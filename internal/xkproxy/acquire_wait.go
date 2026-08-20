package xkproxy

import (
	"context"
	"errors"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

// acquireWaitPollInterval paces the wait for a fresh exit. The collector
// republishes the pool on its own cycle (5s by default), so polling faster only
// burns CPU while polling slower would add latency after the pool recovers.
const acquireWaitPollInterval = 250 * time.Millisecond

// AcquireWithWait acquires a proxy handle and waits out a momentarily empty
// pool instead of failing the caller on the spot. Only ReasonNoHealthyProxy is
// worth waiting for: every other failure describes a closed, disabled or
// misconfigured manager that will not fix itself inside one request.
//
// The wait deliberately lives here rather than inside Manager.Acquire because
// Switcher.Acquire holds its read lock for the whole call, so blocking in there
// would freeze an administrator's proxy config switch for the entire budget.
func AcquireWithWait(ctx context.Context, provider Provider, snapshot runtimeconfig.Snapshot, session string, budget time.Duration) (*Handle, error) {
	if provider == nil {
		return nil, errors.New("acquire proxy: provider is nil")
	}
	handle, err := provider.Acquire(ctx, snapshot, session)
	if err == nil || budget <= 0 || !waitableProxyError(err) {
		return handle, err
	}
	deadline := time.Now().Add(budget)
	ticker := time.NewTicker(acquireWaitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		handle, err = provider.Acquire(ctx, snapshot, session)
		if err == nil || !waitableProxyError(err) {
			return handle, err
		}
		if !time.Now().Before(deadline) {
			return nil, err
		}
	}
}

// waitableProxyError reports whether the pool is merely empty right now. An
// empty pool is transient by construction: leases expire on a TTL and the
// collector replaces them, so the next cycle is likely to publish a usable exit.
func waitableProxyError(err error) bool {
	var proxyErr *Error
	if !errors.As(err, &proxyErr) {
		return false
	}
	return proxyErr.Reason() == ReasonNoHealthyProxy
}
