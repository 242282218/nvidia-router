package pool

import (
	"context"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

// TestStreamingQuotaPerKey verifies the R2.1 isolation: a single key accepts at
// most max_streaming_per_key concurrent streaming leases, and the third
// streaming request cannot acquire.
func TestStreamingQuotaPerKey(t *testing.T) {
	p := New(testSettings{}, fakeClock{now: time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)})
	p.LoadSnapshot(testKeys(1), nil)

	first, ok := p.tryAcquireStream(1, nil)
	if !ok {
		t.Fatal("first streaming lease was not granted")
	}
	second, ok := p.tryAcquireStream(1, nil)
	if !ok {
		t.Fatal("second streaming lease was not granted")
	}
	if _, ok := p.tryAcquireStream(1, nil); ok {
		t.Fatal("third streaming lease exceeded the per-key streaming quota")
	}
	first.Release()
	if _, ok := p.tryAcquireStream(1, nil); !ok {
		t.Fatal("streaming slot was not freed by Release")
	}
	second.Release()
}

// TestStreamingQuotaIsolatesShortRequests locks in the R4 fix: streaming leases
// draw from their own per-key quota and must not occupy the single busy slot,
// so short requests keep flowing while streams are in flight — and vice versa.
func TestStreamingQuotaIsolatesShortRequests(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), nil)

	stream, ok := p.tryAcquireStream(1, nil)
	if !ok {
		t.Fatal("streaming lease was not granted")
	}
	defer stream.Release()
	// A short request must still acquire while a stream holds the key.
	short, ok := p.tryAcquire(1, nil)
	if !ok {
		t.Fatal("short request blocked by an in-flight stream")
	}
	short.Release()

	// A stream must still acquire while a short request holds the busy slot.
	blocker, ok := p.tryAcquire(1, nil)
	if !ok {
		t.Fatal("busy slot was not granted")
	}
	defer blocker.Release()
	second, ok := p.tryAcquireStream(1, nil)
	if !ok {
		t.Fatal("stream blocked by an in-flight short request")
	}
	second.Release()
}

// TestStreamingQuotaRoundRobinSkipsFullKey verifies a streaming request walks to
// the next key when the round-robin cursor lands on a key at its streaming cap.
func TestStreamingQuotaRoundRobinSkipsFullKey(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1, 2), nil)

	// Saturate key 1's streaming quota directly: round-robin normally distributes
	// streams across keys, so the skip path needs an explicitly full key.
	p.keys[1].streamingBusy = 2

	lease, ok := p.tryAcquireStream(1, nil)
	if !ok {
		t.Fatal("streaming lease should skip the saturated key 1 and use key 2")
	}
	defer lease.Release()
	if got := lease.KeyID(); got != 2 {
		t.Fatalf("streaming lease key = %d, want 2", got)
	}
}

// TestStreamingQueueServesThirdWaiterAfterRelease covers the acceptance case
// "third streaming request queues" at the queue layer: the queued stream is
// granted once an earlier stream releases its slot.
func TestStreamingQueueServesThirdWaiterAfterRelease(t *testing.T) {
	p := newQueueTestPool(runtimeconfig.Snapshot{QueueCapacity: 10, QueueWaitTimeoutMS: 1000, MaxStreamingPerKey: 2}, 1)
	first, err := p.Acquire(context.Background(), 1, nil, true)
	if err != nil {
		t.Fatalf("first streaming Acquire: %v", err)
	}
	defer first.Release()
	second, err := p.Acquire(context.Background(), 1, nil, true)
	if err != nil {
		t.Fatalf("second streaming Acquire: %v", err)
	}

	third := acquireAsyncStream(p, context.Background(), 1)
	waitForQueueLength(t, p, 1)

	// Releasing the second stream must let the queued third stream through.
	second.Release()
	result := receiveAcquire(t, third)
	if result.err != nil {
		t.Fatalf("queued streaming Acquire: %v", result.err)
	}
	result.lease.Release()
}

func acquireAsyncStream(p *Pool, ctx context.Context, modelID int64) <-chan acquireCallResult {
	result := make(chan acquireCallResult, 1)
	go func() {
		lease, err := p.Acquire(ctx, modelID, nil, true)
		result <- acquireCallResult{lease: lease, err: err}
	}()
	return result
}

// TestStreamingReleaseRestoresBusySlotForShortRequests guards the lease-release
// accounting: a released streaming lease decrements only the streaming counter,
// leaving the busy slot untouched.
func TestStreamingReleaseRestoresBusySlotForShortRequests(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), nil)

	stream, ok := p.tryAcquireStream(1, nil)
	if !ok {
		t.Fatal("streaming lease was not granted")
	}
	stream.Release()
	short, ok := p.tryAcquire(1, nil)
	if !ok {
		t.Fatal("short request blocked after streaming lease release")
	}
	short.Release()
}
