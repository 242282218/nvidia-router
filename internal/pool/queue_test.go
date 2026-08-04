package pool

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/runtimeconfig"
)

func TestQueueServesWaitersInStrictFIFOOrder(t *testing.T) {
	settings := &countingSettings{snapshot: queueSnapshot(10, time.Second)}
	p := New(settings, clock.RealClock{})
	p.LoadSnapshot(testKeys(1), nil)
	holder := mustAcquire(t, p, 1)

	first := acquireAsync(p, context.Background(), 1)
	waitForQueueLength(t, p, 1)
	second := acquireAsync(p, context.Background(), 1)
	waitForQueueLength(t, p, 2)

	holder.Release()
	firstResult := receiveAcquire(t, first)
	if firstResult.err != nil {
		t.Fatalf("first Acquire: %v", firstResult.err)
	}
	select {
	case result := <-second:
		result.release()
		t.Fatalf("second waiter completed before first released: %v", result.err)
	case <-time.After(20 * time.Millisecond):
	}
	firstResult.lease.Release()
	secondResult := receiveAcquire(t, second)
	if secondResult.err != nil {
		t.Fatalf("second Acquire: %v", secondResult.err)
	}
	secondResult.lease.Release()

	if got := settings.reads.Load(); got != 3 {
		t.Fatalf("settings Snapshot reads = %d, want 3", got)
	}
}

func TestQueueCancellationRemovesHeadWithoutBlockingNext(t *testing.T) {
	p := newQueueTestPool(queueSnapshot(10, time.Second), 1)
	holder := mustAcquire(t, p, 1)
	firstContext, cancelFirst := context.WithCancel(context.Background())
	first := acquireAsync(p, firstContext, 1)
	waitForQueueLength(t, p, 1)
	second := acquireAsync(p, context.Background(), 1)
	waitForQueueLength(t, p, 2)

	cancelFirst()
	if err := receiveAcquire(t, first).err; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Acquire error = %v, want context.Canceled", err)
	}
	waitForQueueLength(t, p, 1)
	holder.Release()
	result := receiveAcquire(t, second)
	if result.err != nil {
		t.Fatalf("second Acquire after cancellation: %v", result.err)
	}
	result.lease.Release()
}

func TestQueueCapacityAndTimeoutReturnDistinctRetryableErrors(t *testing.T) {
	p := newQueueTestPool(queueSnapshot(1, 30*time.Millisecond), 1)
	holder := mustAcquire(t, p, 1)
	queued := acquireAsync(p, context.Background(), 1)
	waitForQueueLength(t, p, 1)

	_, err := p.Acquire(context.Background(), 1, nil)
	assertAPIError(t, err, http.StatusTooManyRequests, "queue_full", true)
	assertAPIError(t, receiveAcquire(t, queued).err, http.StatusTooManyRequests, "queue_timeout", true)
	holder.Release()
}

func TestQueueDefaultsToCapacity100(t *testing.T) {
	p := newQueueTestPool(runtimeconfig.Snapshot{}, 1)
	holder := mustAcquire(t, p, 1)
	results := make([]<-chan acquireCallResult, 0, 100)
	cancels := make([]context.CancelFunc, 0, 100)
	for index := range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		results = append(results, acquireAsync(p, ctx, 1))
		waitForQueueLength(t, p, index+1)
	}

	_, err := p.Acquire(context.Background(), 1, nil)
	assertAPIError(t, err, http.StatusTooManyRequests, "queue_full", true)
	for _, cancel := range cancels {
		cancel()
	}
	for _, result := range results {
		if err := receiveAcquire(t, result).err; !errors.Is(err, context.Canceled) {
			t.Fatalf("default-capacity waiter error = %v, want context.Canceled", err)
		}
	}
	holder.Release()
}

func TestAcquireClassifiesUnavailableKeys(t *testing.T) {
	now := time.Now()
	cooling := now.Add(time.Minute)
	tests := []struct {
		name   string
		keys   []keystate.KeySnapshot
		blocks []keystate.ModelBlock
		status int
		code   string
		retry  bool
	}{
		{name: "disabled", keys: []keystate.KeySnapshot{{ID: 1}}, status: http.StatusServiceUnavailable, code: "no_available_keys"},
		{name: "auth invalid", keys: []keystate.KeySnapshot{{ID: 1, Enabled: true, AuthInvalid: true}}, status: http.StatusServiceUnavailable, code: "no_available_keys"},
		{name: "cooling", keys: []keystate.KeySnapshot{{ID: 1, Enabled: true, CooldownUntil: &cooling}}, status: http.StatusTooManyRequests, code: "all_keys_cooling_down", retry: true},
		{name: "model blocked", keys: testKeys(1), blocks: []keystate.ModelBlock{{KeyID: 1, ModelID: 9}}, status: http.StatusNotFound, code: "model_not_available"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newQueueTestPool(queueSnapshot(10, time.Second))
			p.LoadSnapshot(tt.keys, tt.blocks)
			_, err := p.Acquire(context.Background(), 9, nil)
			assertAPIError(t, err, tt.status, tt.code, tt.retry)
		})
	}
}

func TestQueueRotatesModelAwareHeadAndResolvesFullyBlockedHead(t *testing.T) {
	p := newQueueTestPool(queueSnapshot(10, time.Second), 1, 2)
	p.SetModelBlock(2, 10, true)
	holder := mustAcquire(t, p, 10)
	first := acquireAsync(p, context.Background(), 10)
	waitForQueueLength(t, p, 1)
	second := acquireAsync(p, context.Background(), 20)
	// The model-10 head can only use the busy key 1, so a later waiter that
	// can use key 2 must not be stalled behind it.
	secondResult := receiveAcquire(t, second)
	if secondResult.err != nil {
		t.Fatalf("later model behind busy head: %v", secondResult.err)
	}
	if got := secondResult.lease.KeyID(); got != 2 {
		t.Fatalf("second Lease key = %d, want 2", got)
	}
	secondResult.lease.Release()

	// Once every key is blocked for the head's model it resolves immediately.
	p.SetModelBlock(1, 10, true)
	assertAPIError(t, receiveAcquire(t, first).err, http.StatusNotFound, "model_not_available", false)
	holder.Release()
}

func TestQueueBusyHeadRotatesSoLaterWaiterProceeds(t *testing.T) {
	p := newQueueTestPool(queueSnapshot(10, time.Second), 1, 2)
	holderA := mustAcquire(t, p, 1)
	holderB := mustAcquireWithAttempted(t, p, 1, map[int64]struct{}{1: {}})
	// Both keys are busy. The head waiter already tried key 1 (its retry
	// excludes it) and key 2 is busy, so it can only see UnavailableBusy.
	first := acquireAsyncWithAttempted(p, context.Background(), 1, map[int64]struct{}{1: {}})
	waitForQueueLength(t, p, 1)
	second := acquireAsync(p, context.Background(), 1)
	waitForQueueLength(t, p, 2)

	// Freeing key 1 cannot serve the busy-stalled head, so the later waiter
	// must get it instead of being stalled behind the whole queue.
	holderA.Release()
	secondResult := receiveAcquire(t, second)
	if secondResult.err != nil {
		t.Fatalf("second Acquire behind busy head: %v", secondResult.err)
	}
	if got := secondResult.lease.KeyID(); got != 1 {
		t.Fatalf("second Lease key = %d, want 1", got)
	}
	secondResult.lease.Release()
	holderB.Release()

	firstResult := receiveAcquire(t, first)
	if firstResult.err != nil {
		t.Fatalf("first Acquire after rotation: %v", firstResult.err)
	}
	if got := firstResult.lease.KeyID(); got != 2 {
		t.Fatalf("first Lease key = %d, want 2", got)
	}
	firstResult.lease.Release()
}

func TestShutdownRejectsQueuedAndFutureAcquire(t *testing.T) {
	p := newQueueTestPool(queueSnapshot(10, time.Second), 1)
	holder := mustAcquire(t, p, 1)
	waiter := acquireAsync(p, context.Background(), 1)
	waitForQueueLength(t, p, 1)

	p.Shutdown()
	p.Shutdown()
	assertAPIError(t, receiveAcquire(t, waiter).err, http.StatusServiceUnavailable, "server_shutting_down", false)
	_, err := p.Acquire(context.Background(), 1, nil)
	assertAPIError(t, err, http.StatusServiceUnavailable, "server_shutting_down", false)
	holder.Release()
}

type acquireCallResult struct {
	lease Lease
	err   error
}

func (r acquireCallResult) release() {
	if r.lease != nil {
		r.lease.Release()
	}
}

func acquireAsync(p *Pool, ctx context.Context, modelID int64) <-chan acquireCallResult {
	return acquireAsyncWithAttempted(p, ctx, modelID, nil)
}

func acquireAsyncWithAttempted(p *Pool, ctx context.Context, modelID int64, attempted map[int64]struct{}) <-chan acquireCallResult {
	result := make(chan acquireCallResult, 1)
	go func() {
		lease, err := p.Acquire(ctx, modelID, attempted)
		result <- acquireCallResult{lease: lease, err: err}
	}()
	return result
}

func mustAcquire(t *testing.T, p *Pool, modelID int64) Lease {
	t.Helper()
	return mustAcquireWithAttempted(t, p, modelID, nil)
}

func mustAcquireWithAttempted(t *testing.T, p *Pool, modelID int64, attempted map[int64]struct{}) Lease {
	t.Helper()
	lease, err := p.Acquire(context.Background(), modelID, attempted)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return lease
}

func receiveAcquire(t *testing.T, result <-chan acquireCallResult) acquireCallResult {
	t.Helper()
	select {
	case received := <-result:
		return received
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Acquire")
		return acquireCallResult{}
	}
}

func waitForQueueLength(t *testing.T, p *Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := p.waiters.Len()
		p.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue length did not become %d", want)
}

func assertAPIError(t *testing.T, err error, status int, code string, retry bool) {
	t.Helper()
	var publicError *apierror.Error
	if !errors.As(err, &publicError) {
		t.Fatalf("error = %T %v, want *apierror.Error", err, err)
	}
	if publicError.Status != status || publicError.Code != code {
		t.Fatalf("error = status %d code %q, want status %d code %q", publicError.Status, publicError.Code, status, code)
	}
	if retry != (publicError.RetryAfter > 0) {
		t.Fatalf("error RetryAfter = %s, retry expectation = %t", publicError.RetryAfter, retry)
	}
}

func newQueueTestPool(snapshot runtimeconfig.Snapshot, ids ...int64) *Pool {
	p := New(queueSettings{snapshot: snapshot}, clock.RealClock{})
	p.LoadSnapshot(testKeys(ids...), nil)
	return p
}

func queueSnapshot(capacity int, wait time.Duration) runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{QueueCapacity: capacity, QueueWaitTimeoutMS: int(wait / time.Millisecond)}
}

type queueSettings struct {
	snapshot runtimeconfig.Snapshot
}

func (s queueSettings) Snapshot() runtimeconfig.Snapshot { return s.snapshot }

type countingSettings struct {
	snapshot runtimeconfig.Snapshot
	reads    atomic.Int32
}

func (s *countingSettings) Snapshot() runtimeconfig.Snapshot {
	s.reads.Add(1)
	return s.snapshot
}
