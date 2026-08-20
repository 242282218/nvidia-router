package xkproxy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

type waitProviderFake struct {
	calls    atomic.Int32
	failures int32
	err      error
}

func (p *waitProviderFake) Configured() bool { return true }

func (p *waitProviderFake) Enabled() bool { return true }

func (p *waitProviderFake) Acquire(context.Context, runtimeconfig.Snapshot, string) (*Handle, error) {
	if p.calls.Add(1) <= p.failures {
		return nil, p.err
	}
	return &Handle{}, nil
}

func TestAcquireWithWaitRetriesUntilPoolRefills(t *testing.T) {
	provider := &waitProviderFake{failures: 1, err: NewNoHealthyProxyError()}
	handle, err := AcquireWithWait(context.Background(), provider, runtimeconfig.Snapshot{}, "session", 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireWithWait: %v", err)
	}
	if handle == nil {
		t.Fatal("AcquireWithWait returned a nil handle without an error")
	}
	if calls := provider.calls.Load(); calls != 2 {
		t.Fatalf("Acquire calls = %d, want 2 (one failure then the refilled pool)", calls)
	}
}

func TestAcquireWithWaitGivesUpAfterBudget(t *testing.T) {
	provider := &waitProviderFake{failures: 1000, err: NewNoHealthyProxyError()}
	started := time.Now()
	_, err := AcquireWithWait(context.Background(), provider, runtimeconfig.Snapshot{}, "session", 300*time.Millisecond)
	elapsed := time.Since(started)
	var proxyErr *Error
	if !errors.As(err, &proxyErr) || proxyErr.Reason() != ReasonNoHealthyProxy {
		t.Fatalf("error = %v, want a no_healthy_proxy proxy error", err)
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("gave up after %s, want at least the 300ms budget", elapsed)
	}
}

// A transport failure means an exit existed and broke, so waiting for a new IP
// would only add latency: the caller's own retry loop picks a different exit.
func TestAcquireWithWaitDoesNotWaitForTransportFailures(t *testing.T) {
	provider := &waitProviderFake{failures: 1, err: NewTransportError(errors.New("dial failed"))}
	started := time.Now()
	if _, err := AcquireWithWait(context.Background(), provider, runtimeconfig.Snapshot{}, "session", time.Minute); err == nil {
		t.Fatal("AcquireWithWait waited out a transport failure instead of returning it")
	}
	if elapsed := time.Since(started); elapsed > acquireWaitPollInterval {
		t.Fatalf("returned after %s, want an immediate failure", elapsed)
	}
	if calls := provider.calls.Load(); calls != 1 {
		t.Fatalf("Acquire calls = %d, want 1", calls)
	}
}

func TestAcquireWithWaitStopsOnCancelledContext(t *testing.T) {
	provider := &waitProviderFake{failures: 1000, err: NewNoHealthyProxyError()}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := AcquireWithWait(ctx, provider, runtimeconfig.Snapshot{}, "session", time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestAcquireWithWaitRejectsNilProvider(t *testing.T) {
	if _, err := AcquireWithWait(context.Background(), nil, runtimeconfig.Snapshot{}, "", time.Second); err == nil {
		t.Fatal("AcquireWithWait accepted a nil provider")
	}
}
