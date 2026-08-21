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
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/runtimeconfig"
)

// queueLimitPool hands out one lease, then every further acquire blocks until
// the request context expires (simulating a request sitting in the pool queue
// when the total budget runs out) and returns the context error, which the
// Attempt maps to a 429 queue_timeout.
type queueLimitPool struct {
	calls atomic.Int32
}

func (p *queueLimitPool) AcquireWithSnapshot(ctx context.Context, _ int64, _ map[int64]struct{}, _ runtimeconfig.Snapshot, _ bool) (pool.Lease, error) {
	if p.calls.Add(1) == 1 {
		return testLease{keyID: 1}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *queueLimitPool) ApplySuccess(int64)                                           {}
func (p *queueLimitPool) ApplyFailure(int64, int64, fault.Fault, keystate.KeySnapshot) {}

type testLease struct{ keyID int64 }

func (l testLease) KeyID() int64 { return l.keyID }
func (l testLease) Release()     {}

// TestRunPrefersQueueTimeoutOverStaleFault locks in the fix for the queue-vs-
// budget race: when the total deadline expires while the request waits in the
// pool queue after an earlier upstream fault, the honest answer is the 429
// queue_timeout (with Retry-After aligned to the queue-wait setting), NOT the
// stale upstream 5xx from the first attempt.
func TestRunPrefersQueueTimeoutOverStaleFault(t *testing.T) {
	settings := &countingProvider{snapshot: runtimeconfig.Snapshot{
		QueueCapacity: 10, QueueWaitTimeoutMS: 8000, ConnectTimeoutMS: 1000,
		FirstByteTimeoutMS: 5000, NonstreamTotalTimeoutMS: 300, MaxAttemptsPerRequest: 3,
	}}
	keyPool := &queueLimitPool{}
	states := newAttemptStateWriter(time.Now())
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})

	_, err := attempt.Run(context.Background(), 10, false, func(_ context.Context, keyID int64, _ []byte, _ *CommitState) (*http.Response, error) {
		if keyID == 1 {
			return attemptResponse(500, ""), nil
		}
		return attemptResponse(200, `{"ok":true}`), nil
	})

	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Run error = %T %v, want *apierror.Error", err, err)
	}
	if apiErr.Code != "queue_timeout" {
		t.Fatalf("error code = %q, want queue_timeout (the stale upstream 500 must not win)", apiErr.Code)
	}
	if apiErr.RetryAfter != 8*time.Second {
		t.Fatalf("RetryAfter = %s, want 8s (aligned with QueueWaitTimeoutMS)", apiErr.RetryAfter)
	}
}
