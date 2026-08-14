package router

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/runtimeconfig"
)

// TestAttemptStopsAtMaxAttempts locks in the retry cap. Before it, the only
// bound was the pool size, so a large key pool turned a single client request
// into that many upstream calls.
func TestAttemptStopsAtMaxAttempts(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000, NonstreamTotalTimeoutMS: 60000,
		MaxAttemptsPerRequest: 3, RetryBudgetMS: 60000,
	}}
	keyPool := newAttemptPool(settings, 1, 2, 3, 4, 5, 6, 7, 8)
	states := newAttemptStateWriter(time.Now())
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})

	calls := 0
	_, err := attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		calls++
		return attemptResponse(500, ""), nil
	})
	if err == nil {
		t.Fatal("expected failure after the attempt cap")
	}
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want 3 (cap), pool had 8 keys", calls)
	}
}

// TestAttemptStreamAcquireBoundedByRetryDeadline locks in that a streaming
// request's pre-commit queue wait honours the retry window. A stream carries no
// total deadline, so before this fix its acquire waited on the pool's queue
// timer alone: an operator who set queue_wait_timeout_ms above retry_budget_ms
// let the request sit in the queue long after the pre-commit phase was supposed
// to end, occupying a queue slot with no way for the loop's retryDeadline check
// to fire. The retry deadline must bound the queue phase and surface the same
// retryable queue_timeout the non-stream path already produces.
func TestAttemptStreamAcquireBoundedByRetryDeadline(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	settings.snapshot.QueueWaitTimeoutMS = 5000 // longer than the retry window
	settings.snapshot.RetryBudgetMS = 100
	settings.snapshot.MaxAttemptsPerRequest = 3
	keyPool := newAttemptPool(settings, 1)
	// Saturate the per-key streaming budget (default MaxStreamingPerKey=2) so the
	// request under test must queue. A non-stream busy slot does not block stream
	// acquires (audit R4: streams draw from an independent per-key budget), so
	// holding key 1 non-stream would let the stream through immediately.
	for i := 0; i < 2; i++ {
		lease, err := keyPool.Acquire(context.Background(), 1, nil, true)
		if err != nil {
			t.Fatalf("hold streaming slot %d: %v", i, err)
		}
		defer lease.Release()
	}
	attempt := NewAttempt(settings, keyPool, testSecrets{}, newAttemptStateWriter(time.Now()), keyPool, clock.RealClock{})
	var calls atomic.Int32

	started := time.Now()
	_, err := attempt.Run(context.Background(), 1, true, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		calls.Add(1)
		return attemptResponse(200, ""), nil
	})
	var publicError *apierror.Error
	if !errors.As(err, &publicError) {
		t.Fatalf("Run error = %T %v, want *apierror.Error", err, err)
	}
	if publicError.Status != http.StatusTooManyRequests || publicError.Code != "queue_timeout" {
		t.Fatalf("Run error = status %d code %q, want 429 queue_timeout", publicError.Status, publicError.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("Execute calls = %d, want 0 (the request never left the queue)", calls.Load())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("stream queue wait elapsed = %s, want the 100ms retry deadline to fire far before the 5s queue wait", elapsed)
	}
}

// TestAttemptStreamRetryLoopIsBounded is the core regression: a streaming
// request carries no total deadline, which previously left its retry loop
// unbounded even though each individual attempt was bounded.
func TestAttemptStreamRetryLoopIsBounded(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000,
		MaxAttemptsPerRequest: 4, RetryBudgetMS: 60000,
	}}
	keyPool := newAttemptPool(settings, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	states := newAttemptStateWriter(time.Now())
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})

	calls := 0
	_, err := attempt.Run(context.Background(), 1, true, func(ctx context.Context, _ int64, _ []byte, _ *CommitState) (*http.Response, error) {
		calls++
		// A stream must not carry a total deadline; long generations are legal.
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("stream Execute context must not carry a total deadline")
		}
		return attemptResponse(503, ""), nil
	})
	if err == nil {
		t.Fatal("expected failure after the stream attempt cap")
	}
	if calls != 4 {
		t.Fatalf("stream upstream calls = %d, want 4 (cap), pool had 10 keys", calls)
	}
}

// TestAttemptRetryBudgetStopsLoop verifies the pre-commit time bound stops the
// loop even when the attempt cap has not been reached.
func TestAttemptRetryBudgetStopsLoop(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000,
		MaxAttemptsPerRequest: 50, RetryBudgetMS: 1000,
	}}
	keyPool := newAttemptPool(settings, 1, 2, 3, 4, 5, 6)
	states := newAttemptStateWriter(time.Now())
	source := newRetryClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, source)

	calls := 0
	_, err := attempt.Run(context.Background(), 1, true, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		calls++
		// Each attempt burns 400ms of the 1000ms retry budget.
		source.advance(400 * time.Millisecond)
		return attemptResponse(500, ""), nil
	})
	if err == nil {
		t.Fatal("expected failure once the retry budget expired")
	}
	if calls > 4 {
		t.Fatalf("upstream calls = %d, want the retry budget to stop the loop well before the 50-attempt cap", calls)
	}
	if calls < 2 {
		t.Fatalf("upstream calls = %d, want at least one retry inside the budget", calls)
	}
}

// TestAttemptStreamBudgetNeverBoundsCommittedStream guards the reason the bound
// is checked in the loop rather than on the request context: a committed stream
// must keep running past the retry budget.
func TestAttemptStreamBudgetNeverBoundsCommittedStream(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 1000,
	}}
	keyPool := newAttemptPool(settings, 1, 2, 3)
	states := newAttemptStateWriter(time.Now())
	source := newRetryClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, source)

	var streamCtx context.Context
	result, err := attempt.Run(context.Background(), 1, true, func(ctx context.Context, _ int64, _ []byte, commit *CommitState) (*http.Response, error) {
		streamCtx = ctx
		commit.Commit()
		return attemptResponse(200, "data: hi\n\n"), nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer result.Release()
	defer func() { _ = result.Response.Body.Close() }()

	// Push the clock far past the retry budget: the stream context must survive.
	source.advance(10 * time.Minute)
	if streamCtx.Err() != nil {
		t.Fatalf("committed stream context was cancelled by the retry budget: %v", streamCtx.Err())
	}
}

// TestAttemptFirstRetryIsImmediate keeps ordinary single-bad-key failover fast:
// backoff is for a struggling upstream, not for one dead credential.
func TestAttemptFirstRetryIsImmediate(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000, NonstreamTotalTimeoutMS: 60000,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 60000,
	}}
	keyPool := newAttemptPool(settings, 1, 2)
	states := newAttemptStateWriter(time.Now())
	source := newRetryClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, source)

	result, err := attempt.Run(context.Background(), 1, false, func(_ context.Context, keyID int64, _ []byte, _ *CommitState) (*http.Response, error) {
		if keyID == 1 {
			return attemptResponse(500, ""), nil
		}
		return attemptResponse(200, `{"ok":true}`), nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer result.Release()
	if source.timers != 0 {
		t.Fatalf("backoff timers on first retry = %d, want 0 (first failover must be immediate)", source.timers)
	}
}

// TestAttemptBacksOffOnRepeatedFailures verifies escalation does happen once
// failures repeat, which is the signal the upstream itself is unwell.
func TestAttemptBacksOffOnRepeatedFailures(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000, NonstreamTotalTimeoutMS: 600000,
		MaxAttemptsPerRequest: 4, RetryBudgetMS: 600000,
	}}
	keyPool := newAttemptPool(settings, 1, 2, 3, 4)
	states := newAttemptStateWriter(time.Now())
	source := newRetryClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, source)

	_, err := attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		return attemptResponse(503, ""), nil
	})
	if err == nil {
		t.Fatal("expected failure after the attempt cap")
	}
	// 4 attempts = 3 retries; the first is immediate, so 2 backoff waits.
	if source.timers != 2 {
		t.Fatalf("backoff timers = %d, want 2 (retries 2 and 3 of 3)", source.timers)
	}
	for _, delay := range source.delays {
		if delay <= 0 {
			t.Fatalf("non-positive backoff delay in %v", source.delays)
		}
		// Jittered base is 0.8..1.2x, and the cap is retryBackoffMax.
		if delay > retryBackoffMax {
			t.Fatalf("backoff delay %s exceeds cap %s", delay, retryBackoffMax)
		}
	}
	if source.delays[1] <= source.delays[0] {
		t.Fatalf("backoff did not escalate: %v", source.delays)
	}
}

// TestBudgetRetryDeadlineNeverExceedsTotal keeps the non-stream contract: the
// total timeout already bounds everything, so the retry window must not outlive it.
func TestBudgetRetryDeadlineNeverExceedsTotal(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	budget := newBudget(runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000,
		NonstreamTotalTimeoutMS: 5000, RetryBudgetMS: 600000,
	}, now, false)
	if budget.retryDeadline.After(budget.totalDeadline) {
		t.Fatalf("retry deadline %v outlived total deadline %v", budget.retryDeadline, budget.totalDeadline)
	}
}

// TestBudgetAppliesDefaultsWhenUnset guards the zero-Snapshot path so an
// uninitialised provider cannot produce a zero cap that rejects every request.
func TestBudgetAppliesDefaultsWhenUnset(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	budget := newBudget(runtimeconfig.Snapshot{}, now, true)
	if budget.maxAttempts != defaultMaxAttemptsPerRequest {
		t.Fatalf("maxAttempts = %d, want default %d", budget.maxAttempts, defaultMaxAttemptsPerRequest)
	}
	if budget.retryDeadline.IsZero() {
		t.Fatal("stream retry deadline must be set even with an empty snapshot")
	}
	if !budget.totalDeadline.IsZero() {
		t.Fatal("stream must not carry a total deadline")
	}
}

// TestBackoffRespectsRetryAfter locks in the R2.3 rhythm: a 429 with a
// Retry-After hint must back off for at least that long before the next key,
// capped at five minutes, instead of the sub-second exponential delay that
// hammered an account-level rate limit before the upstream said it was ready.
func TestBackoffRespectsRetryAfter(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		wantMin    time.Duration
	}{
		{name: "honors retry-after", retryAfter: "10", wantMin: 10 * time.Second},
		{name: "caps retry-after at five minutes", retryAfter: "3600", wantMin: 5 * time.Minute},
		{name: "no retry-after keeps exponential backoff", retryAfter: "", wantMin: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &countingProvider{snapshot: attemptSettings()}
			settings.snapshot.MaxAttemptsPerRequest = 3
			// Give the retry window room: attemptSettings' 10s non-stream total
			// deadline would otherwise cap every backoff below the five-minute
			// Retry-After case.
			settings.snapshot.NonstreamTotalTimeoutMS = 600000
			settings.snapshot.RetryBudgetMS = 600000
			keyPool := newAttemptPool(settings, 1, 2, 3)
			states := newAttemptStateWriter(time.Now())
			source := newRetryClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, source)

			_, err := attempt.Run(context.Background(), 1, false, func(_ context.Context, _ int64, _ []byte, _ *CommitState) (*http.Response, error) {
				response := attemptResponse(429, "")
				if tt.retryAfter != "" {
					response.Header.Set("Retry-After", tt.retryAfter)
				}
				return response, nil
			})
			if err == nil {
				t.Fatal("expected failure after the attempt cap")
			}
				wantDelays := 1
				if tt.retryAfter != "" {
					wantDelays = 2
				}
				if len(source.delays) != wantDelays {
					t.Fatalf("backoff delays = %v, want %d", source.delays, wantDelays)
				}
				delay := source.delays[0]
			if delay < tt.wantMin {
				t.Fatalf("backoff delay %s < %s (Retry-After %q)", delay, tt.wantMin, tt.retryAfter)
			}
			if tt.retryAfter == "" && delay >= 10*time.Second {
				t.Fatalf("backoff delay %s grew to a Retry-After magnitude without a hint", delay)
			}
			if tt.retryAfter == "3600" && delay > 5*time.Minute {
				t.Fatalf("backoff delay %s exceeded the five-minute Retry-After cap", delay)
			}
		})
	}
}

// retryClock is a manual clock that records the backoff waits the attempt loop
// requests, so tests can assert on delays without real sleeping.
type retryClock struct {
	now    time.Time
	timers int
	delays []time.Duration
}

func newRetryClock(now time.Time) *retryClock { return &retryClock{now: now} }

func (c *retryClock) Now() time.Time { return c.now }

func (c *retryClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func (c *retryClock) NewTimer(d time.Duration) *time.Timer {
	c.timers++
	c.delays = append(c.delays, d)
	c.now = c.now.Add(d)
	// Fire immediately: the wait is recorded above and simulated on the clock.
	return time.NewTimer(0)
}

func (c *retryClock) AfterFunc(_ time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(0, callback)
}

var _ = fault.Fault{}
