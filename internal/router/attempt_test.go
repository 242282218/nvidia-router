package router

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

func TestAttemptTriesEachKeyOnceWithCapturedSettingsAndPerAttemptFirstByteBudget(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 2500, FirstByteTimeoutMS: 5000, NonstreamTotalTimeoutMS: 12000,
	}}
	keyPool := newAttemptPool(settings, 1, 2, 3)
	states := newAttemptStateWriter(time.Now())
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})
	var calls []int64
	var deadlines []time.Time
	var totalDeadlines []time.Time
	execute := func(ctx context.Context, keyID int64, _ []byte, _ *CommitState) (*http.Response, error) {
		calls = append(calls, keyID)
		budget, ok := BudgetFromContext(ctx)
		if !ok {
			t.Fatal("Execute context is missing Budget")
		}
		deadlines = append(deadlines, budget.FirstByteDeadline())
		totalDeadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("non-stream Execute context is missing total deadline")
		}
		totalDeadlines = append(totalDeadlines, totalDeadline)
		if budget.ConnectTimeout() != 2500*time.Millisecond {
			t.Fatalf("ConnectTimeout = %s", budget.ConnectTimeout())
		}
		if keyID < 3 {
			return attemptResponse(500, ""), nil
		}
		return attemptResponse(200, `{"ok":true}`), nil
	}

	result, err := attempt.Run(context.Background(), 10, false, execute)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer result.Release()
	if result.Attempts != 3 || result.Lease == nil || result.Lease.KeyID() != 3 {
		t.Fatalf("result = %+v", result)
	}
	if got := settings.reads.Load(); got != 1 {
		t.Fatalf("Snapshot reads = %d, want 1", got)
	}
	if len(calls) != 3 || calls[0] != 1 || calls[1] != 2 || calls[2] != 3 {
		t.Fatalf("Execute calls = %v", calls)
	}
	for index, deadline := range deadlines {
		if deadline.IsZero() {
			t.Fatalf("first-byte deadline %d is zero", index)
		}
		if index > 0 && deadline.Before(deadlines[index-1]) {
			t.Fatalf("first-byte deadlines moved backwards: %v", deadlines)
		}
	}
	for _, deadline := range totalDeadlines[1:] {
		if !deadline.Equal(totalDeadlines[0]) {
			t.Fatalf("total deadlines changed across attempts: %v", totalDeadlines)
		}
	}
}

func TestAttemptSecondRunSkipsPersistedFailureState(t *testing.T) {
	tests := []struct {
		name     string
		response func() *http.Response
	}{
		{name: "401", response: func() *http.Response { return attemptResponse(401, "") }},
		{name: "429", response: func() *http.Response {
			response := attemptResponse(429, "")
			response.Header.Set("Retry-After", "30")
			return response
		}},
		{name: "model 403", response: func() *http.Response { return attemptResponse(403, `{"error":{"message":"forbidden"}}`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &countingProvider{snapshot: attemptSettings()}
			keyPool := newAttemptPool(settings, 1, 2)
			states := newAttemptStateWriter(time.Now())
			attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})
			firstCalls := make([]int64, 0, 2)
			first, err := attempt.Run(context.Background(), 77, false, func(_ context.Context, keyID int64, _ []byte, _ *CommitState) (*http.Response, error) {
				firstCalls = append(firstCalls, keyID)
				if keyID == 1 {
					return tt.response(), nil
				}
				return attemptResponse(200, `{}`), nil
			})
			if err != nil {
				t.Fatalf("first Run: %v", err)
			}
			first.Release()
			if len(firstCalls) != 2 || firstCalls[0] != 1 || firstCalls[1] != 2 {
				t.Fatalf("first calls = %v", firstCalls)
			}

			var secondCalls []int64
			second, err := attempt.Run(context.Background(), 77, false, func(_ context.Context, keyID int64, _ []byte, _ *CommitState) (*http.Response, error) {
				secondCalls = append(secondCalls, keyID)
				return attemptResponse(200, `{}`), nil
			})
			if err != nil {
				t.Fatalf("second Run: %v", err)
			}
			second.Release()
			if len(secondCalls) != 1 || secondCalls[0] != 2 {
				t.Fatalf("second calls = %v, want [2]", secondCalls)
			}
		})
	}
}

func TestAttemptSyncsOnlyAfterStateWriterSucceeds(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			settings := &countingProvider{snapshot: attemptSettings()}
			keyPool := newAttemptPool(settings, 1)
			stateErr := errors.New("state transaction failed")
			states := &failingStateWriter{err: stateErr}
			syncer := &recordingStateSync{}
			attempt := NewAttempt(settings, keyPool, testSecrets{}, states, syncer, clock.RealClock{})

			_, err := attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
				return attemptResponse(status, ""), nil
			})
			if !errors.Is(err, stateErr) {
				t.Fatalf("Run error = %v, want state error", err)
			}
			var upstreamFault fault.Fault
			if errors.As(err, &upstreamFault) {
				t.Fatalf("Run error = %+v, want internal state error", upstreamFault)
			}
			if syncer.calls.Load() != 0 {
				t.Fatal("StateSync ran before failed transaction")
			}
			lease, acquireErr := keyPool.Acquire(context.Background(), 1, nil)
			if acquireErr != nil {
				t.Fatalf("Acquire after state failure: %v", acquireErr)
			}
			lease.Release()
		})
	}
}

func TestAttemptCommittedErrorDoesNotTryAnotherKey(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	keyPool := newAttemptPool(settings, 1, 2)
	attempt := NewAttempt(settings, keyPool, testSecrets{}, newAttemptStateWriter(time.Now()), keyPool, clock.RealClock{})
	var calls atomic.Int32

	_, err := attempt.Run(context.Background(), 1, false, func(_ context.Context, _ int64, _ []byte, commit *CommitState) (*http.Response, error) {
		calls.Add(1)
		writer := commit.Wrap(httptest.NewRecorder())
		writer.WriteHeader(http.StatusOK)
		return nil, io.ErrUnexpectedEOF
	})
	if err == nil {
		t.Fatal("Run succeeded after committed error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Execute calls = %d, want 1", got)
	}
}

func TestAttemptDoesNotPersistProxyFailureOrSwitchKey(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	keyPool := newAttemptPool(settings, 1, 2)
	states := &countingStateWriter{}
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})

	_, err := attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		return nil, xkproxy.NewTransportError(errors.New("private proxy cause"))
	})
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("Run error = %T %v, want proxy error", err, err)
	}
	if states.failures != 0 || states.successes != 0 {
		t.Fatalf("state writes = success:%d failure:%d, want zero", states.successes, states.failures)
	}
	lease, acquireErr := keyPool.Acquire(context.Background(), 1, nil)
	if acquireErr != nil {
		t.Fatalf("Acquire after proxy failure: %v", acquireErr)
	}
	lease.Release()
}

func TestAttemptProxyFailureSkipsStateSyncAndModelBlockPropagation(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	keyPool := newAttemptPool(settings, 1, 2)
	states := &countingStateWriter{}
	syncer := &recordingStateSync{}
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, syncer, clock.RealClock{})

	_, err := attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		return nil, xkproxy.NewTransportError(errors.New("private proxy cause"))
	})
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("Run error = %T %v, want proxy error", err, err)
	}
	if states.failures != 0 || states.successes != 0 {
		t.Fatalf("state writes = success:%d failure:%d, want zero", states.successes, states.failures)
	}
	// The attempt layer has no model-block concept of its own: model blocks are
	// persisted inside MarkFailure and propagated to the in-memory pool through
	// ApplyFailure. A proxy error must invoke neither the state writer nor the
	// state sync, otherwise it would flip consecutive_failures/last_error_code
	// and (re)create or refresh model blocks for the key.
	if got := syncer.calls.Load(); got != 0 {
		t.Fatalf("state sync calls = %d, want 0 (no ApplySuccess/ApplyFailure)", got)
	}
	// The key that hit the proxy error is released untouched and can be
	// acquired again immediately.
	lease, acquireErr := keyPool.Acquire(context.Background(), 1, nil)
	if acquireErr != nil {
		t.Fatalf("Acquire after proxy failure: %v", acquireErr)
	}
	lease.Release()
}

func TestAttemptReturnsLastFaultWhenCandidatesAreExhausted(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	keyPool := newAttemptPool(settings, 1, 2)
	attempt := NewAttempt(settings, keyPool, testSecrets{}, newAttemptStateWriter(time.Now()), keyPool, clock.RealClock{})

	_, err := attempt.Run(context.Background(), 1, false, func(_ context.Context, keyID int64, _ []byte, _ *CommitState) (*http.Response, error) {
		if keyID == 1 {
			return attemptResponse(500, ""), nil
		}
		return attemptResponse(502, ""), nil
	})
	var lastFault fault.Fault
	if !errors.As(err, &lastFault) {
		t.Fatalf("Run error = %T %v, want Fault", err, err)
	}
	if lastFault.HTTPStatus != 502 {
		t.Fatalf("last Fault status = %d, want 502", lastFault.HTTPStatus)
	}
}

func TestAttemptReturnsLastFaultWhenBudgetExpiresInQueue(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	settings.snapshot.FirstByteTimeoutMS = 50
	keyPool := newAttemptPool(settings, 1, 2)
	holder, err := keyPool.Acquire(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("hold first key: %v", err)
	}
	defer holder.Release()
	attempt := NewAttempt(settings, keyPool, testSecrets{}, newAttemptStateWriter(time.Now()), keyPool, clock.RealClock{})

	_, err = attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		return attemptResponse(500, ""), nil
	})
	var lastFault fault.Fault
	if !errors.As(err, &lastFault) {
		t.Fatalf("Run error = %T %v, want last Fault", err, err)
	}
	if lastFault.HTTPStatus != 500 {
		t.Fatalf("last Fault status = %d, want 500", lastFault.HTTPStatus)
	}
}

func TestAttemptClassifiesBudgetExpiryWhileWaitingForFirstKey(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	settings.snapshot.FirstByteTimeoutMS = 50
	keyPool := newAttemptPool(settings, 1)
	holder, err := keyPool.Acquire(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("hold only key: %v", err)
	}
	defer holder.Release()
	attempt := NewAttempt(settings, keyPool, testSecrets{}, newAttemptStateWriter(time.Now()), keyPool, clock.RealClock{})
	var calls atomic.Int32

	_, err = attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		calls.Add(1)
		return attemptResponse(200, ""), nil
	})
	var timeoutFault fault.Fault
	if !errors.As(err, &timeoutFault) {
		t.Fatalf("Run error = %T %v, want timeout Fault", err, err)
	}
	if timeoutFault.HTTPStatus != http.StatusGatewayTimeout || timeoutFault.PublicCode != "upstream_timeout" {
		t.Fatalf("timeout Fault = %+v", timeoutFault)
	}
	if calls.Load() != 0 {
		t.Fatalf("Execute calls = %d, want 0", calls.Load())
	}
}

func TestAttemptNonstreamStateCancellationDoesNotMaskTimeoutFault(t *testing.T) {
	const (
		totalTimeout = 50 * time.Millisecond
		tolerance    = 100 * time.Millisecond
	)
	tests := []struct {
		name    string
		execute ExecuteFunc
	}{
		{
			name: "success persistence",
			execute: func(ctx context.Context, _ int64, _ []byte, _ *CommitState) (*http.Response, error) {
				<-ctx.Done()
				return attemptResponse(http.StatusOK, ""), nil
			},
		},
		{
			name: "failure persistence",
			execute: func(ctx context.Context, _ int64, _ []byte, _ *CommitState) (*http.Response, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &countingProvider{snapshot: attemptSettings()}
			settings.snapshot.NonstreamTotalTimeoutMS = int(totalTimeout / time.Millisecond)
			keyPool := newAttemptPool(settings, 1)
			states := &blockingStateWriter{canceled: make(chan error, 1)}
			attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			started := time.Now()
			_, err := attempt.Run(ctx, 1, false, tt.execute)
			elapsed := time.Since(started)
			var timeoutFault fault.Fault
			if !errors.As(err, &timeoutFault) {
				t.Fatalf("Run error = %T %v, want timeout Fault", err, err)
			}
			if timeoutFault.HTTPStatus != http.StatusGatewayTimeout || timeoutFault.PublicCode != "upstream_timeout" {
				t.Fatalf("timeout Fault = %+v", timeoutFault)
			}
			select {
			case canceledErr := <-states.canceled:
				if !errors.Is(canceledErr, context.DeadlineExceeded) {
					t.Fatalf("state writer cancellation = %v, want context deadline exceeded", canceledErr)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("state writer was not called")
			}
			lease, acquireErr := keyPool.Acquire(context.Background(), 1, nil)
			if acquireErr != nil {
				t.Fatalf("Acquire after persistence cancellation: %v", acquireErr)
			}
			lease.Release()
			if elapsed > totalTimeout+tolerance {
				t.Fatalf("Run elapsed = %s, want at most %s", elapsed, totalTimeout+tolerance)
			}
		})
	}
}

func TestAttemptCommitStateMarksWriteHeaderWriteAndFlush(t *testing.T) {
	tests := []struct {
		name string
		act  func(http.ResponseWriter)
	}{
		{name: "WriteHeader", act: func(writer http.ResponseWriter) { writer.WriteHeader(201) }},
		{name: "Write", act: func(writer http.ResponseWriter) { _, _ = writer.Write([]byte("x")) }},
		{name: "Flush", act: func(writer http.ResponseWriter) { writer.(http.Flusher).Flush() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &CommitState{}
			tt.act(state.Wrap(httptest.NewRecorder()))
			if !state.Committed() {
				t.Fatal("CommitState remained false")
			}
		})
	}
}

func TestAttemptReleasesLeaseWhenExecutePanics(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	keyPool := newAttemptPool(settings, 1)
	attempt := NewAttempt(settings, keyPool, testSecrets{}, newAttemptStateWriter(time.Now()), keyPool, clock.RealClock{})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Run did not propagate panic")
			}
		}()
		_, _ = attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
			panic("execute panic")
		})
	}()

	lease, err := keyPool.Acquire(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("Acquire after panic: %v", err)
	}
	lease.Release()
}

func attemptResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func attemptSettings() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{
		QueueCapacity: 10, QueueWaitTimeoutMS: 1000, ConnectTimeoutMS: 1000,
		FirstByteTimeoutMS: 5000, NonstreamTotalTimeoutMS: 10000,
	}
}

func newAttemptPool(settings runtimeconfig.Provider, ids ...int64) *pool.Pool {
	p := pool.New(settings, clock.RealClock{})
	keys := make([]keystate.KeySnapshot, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, keystate.KeySnapshot{ID: id, Enabled: true})
	}
	p.LoadSnapshot(keys, nil)
	return p
}

type countingProvider struct {
	snapshot runtimeconfig.Snapshot
	reads    atomic.Int32
}

func (p *countingProvider) Snapshot() runtimeconfig.Snapshot {
	p.reads.Add(1)
	return p.snapshot
}

type testSecrets struct{}

func (testSecrets) WithSecret(_ context.Context, keyID int64, callback func([]byte) error) error {
	return callback([]byte{byte(keyID)})
}

type attemptStateWriter struct {
	mu  sync.Mutex
	now time.Time
}

func newAttemptStateWriter(now time.Time) *attemptStateWriter {
	return &attemptStateWriter{now: now}
}

func (w *attemptStateWriter) MarkSuccess(_ context.Context, keyID int64) (keystate.KeySnapshot, error) {
	return keystate.KeySnapshot{ID: keyID, Enabled: true}, nil
}

func (w *attemptStateWriter) MarkFailure(_ context.Context, keyID, _ int64, f fault.Fault) (keystate.KeySnapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	snapshot := keystate.KeySnapshot{ID: keyID, Enabled: true, AuthInvalid: f.DisableKey}
	if f.HTTPStatus == 429 || (f.Retryable && f.HTTPStatus >= 500) {
		until := w.now.Add(time.Minute)
		snapshot.CooldownUntil = &until
	}
	return snapshot, nil
}

type failingStateWriter struct{ err error }

type countingStateWriter struct {
	successes int
	failures  int
}

func (w *countingStateWriter) MarkSuccess(_ context.Context, keyID int64) (keystate.KeySnapshot, error) {
	w.successes++
	return keystate.KeySnapshot{ID: keyID, Enabled: true}, nil
}

func (w *countingStateWriter) MarkFailure(_ context.Context, keyID, _ int64, _ fault.Fault) (keystate.KeySnapshot, error) {
	w.failures++
	return keystate.KeySnapshot{ID: keyID, Enabled: true}, nil
}

func (w *failingStateWriter) MarkSuccess(context.Context, int64) (keystate.KeySnapshot, error) {
	return keystate.KeySnapshot{}, w.err
}

func (w *failingStateWriter) MarkFailure(context.Context, int64, int64, fault.Fault) (keystate.KeySnapshot, error) {
	return keystate.KeySnapshot{}, w.err
}

type blockingStateWriter struct {
	canceled chan error
}

func (w *blockingStateWriter) MarkSuccess(ctx context.Context, _ int64) (keystate.KeySnapshot, error) {
	return w.waitForCancellation(ctx)
}

func (w *blockingStateWriter) MarkFailure(ctx context.Context, _, _ int64, _ fault.Fault) (keystate.KeySnapshot, error) {
	return w.waitForCancellation(ctx)
}

func (w *blockingStateWriter) waitForCancellation(ctx context.Context) (keystate.KeySnapshot, error) {
	<-ctx.Done()
	w.canceled <- ctx.Err()
	return keystate.KeySnapshot{}, ctx.Err()
}

type recordingStateSync struct {
	calls atomic.Int32
}

func (*recordingStateSync) LoadSnapshot([]keystate.KeySnapshot, []keystate.ModelBlock) {}
func (*recordingStateSync) UpsertKey(keystate.KeySnapshot)                             {}
func (*recordingStateSync) RemoveKey(int64)                                            {}
func (s *recordingStateSync) ApplySuccess(int64)                                       { s.calls.Add(1) }
func (s *recordingStateSync) ApplyFailure(int64, int64, fault.Fault, keystate.KeySnapshot) {
	s.calls.Add(1)
}
func (*recordingStateSync) SetModelBlock(int64, int64, bool) {}
