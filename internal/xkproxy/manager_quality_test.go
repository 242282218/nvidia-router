package xkproxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func newPoolManager(t *testing.T, proxies []Proxy) *Manager {
	t.Helper()
	base := http.DefaultTransport.(*http.Transport)
	manager, err := NewWithPool(CollectorConfig{UpstreamTimeout: time.Second}, "proxy-secret", base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewWithPool: %v", err)
	}
	t.Cleanup(manager.Close)
	if len(proxies) > 0 {
		manager.pool.Replace(proxies)
	}
	return manager
}

// TestManagerPoolEmptyIsRetryableNotKeyFault proves the pool-empty case surfaces
// ReasonNoHealthyProxy (audit D3): the router turns it into a retryable 503 and
// must not cooldown the key, because the request never reached the upstream.
func TestManagerPoolEmptyIsRetryableNotKeyFault(t *testing.T) {
	manager := newPoolManager(t, nil) // empty pool, no cached transport yet

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	_, err := manager.Acquire(context.Background(), snapshot, "session-1")
	if err == nil {
		t.Fatal("Acquire on an empty pool succeeded, want an error")
	}
	var proxyErr *Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("error = %T %v, want *xkproxy.Error", err, err)
	}
	if proxyErr.Reason() != ReasonNoHealthyProxy {
		t.Fatalf("reason = %q, want %q", proxyErr.Reason(), ReasonNoHealthyProxy)
	}
}

// TestManagerStickyRebindReSelectsProxy proves a session pinned to one exit for
// longer than stickyRebindInterval is rebuilt against a fresh pool selection
// (audit H9), so a quietly-degraded exit cannot hold a key forever.
func TestManagerStickyRebindReSelectsProxy(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{
		{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)},
		{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute)},
	})

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	first, err := manager.Acquire(context.Background(), snapshot, "session-rebind")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstKey := first.proxyKey

	// Age the cache entry past the rebind window so the next Acquire re-selects.
	key := transportKey{connectTimeoutMS: 1000, firstByteTimeoutMS: 2000, session: "session-rebind"}
	entry := manager.transports[key]
	if entry == nil {
		t.Fatal("first Acquire did not create a cached transport")
	}
	entry.createdAt = time.Now().Add(-2 * stickyRebindInterval)

	rebound, err := manager.Acquire(context.Background(), snapshot, "session-rebind")
	if err != nil {
		t.Fatalf("rebind Acquire: %v", err)
	}
	t.Cleanup(rebound.Release)
	if rebound.Transport() == first.Transport() {
		t.Fatal("rebind re-used the same transport; session stayed pinned past the rebind window")
	}
	if rebound.proxyKey == firstKey {
		t.Fatalf("rebind kept proxy %q, want a fresh pool selection", firstKey)
	}
	first.Release()
}

// TestManagerProxyTransportEnablesHTTP2 guards the h2 setting on the proxy
// transport: the NVIDIA target inside the CONNECT tunnel serves HTTP/2, and
// disabling h2 made every proxied request fail with a malformed-response error
// (found in real 联调 2026-08-12). Direct mode already enables h2; the proxy
// path must match.
func TestManagerProxyTransportEnablesHTTP2(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{{
		Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute),
	}})

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	handle, err := manager.Acquire(context.Background(), snapshot, "session-h2")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(handle.Release)
	transport, ok := handle.Transport().(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", handle.Transport())
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("proxy transport must enable HTTP/2 for the tunnel target")
	}
}

// TestManagerStickyRebindSkipsSameProxy proves a stale session is NOT rebuilt
// when the pool's best exit is the one it already uses: rebinding would only
// re-CONNECT for nothing (audit H9).
func TestManagerStickyRebindSkipsSameProxy(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{
		{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)},
	})

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	first, err := manager.Acquire(context.Background(), snapshot, "session-single")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstKey := first.proxyKey

	key := transportKey{connectTimeoutMS: 1000, firstByteTimeoutMS: 2000, session: "session-single"}
	entry := manager.transports[key]
	if entry == nil {
		t.Fatal("first Acquire did not create a cached transport")
	}
	entry.createdAt = time.Now().Add(-2 * stickyRebindInterval)

	// The only proxy in the pool is also the best proxy: the rebind must skip.
	reused, err := manager.Acquire(context.Background(), snapshot, "session-single")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	t.Cleanup(reused.Release)
	if reused.Transport() != first.Transport() {
		t.Fatal("single-proxy pool rebuilt the transport despite no alternative")
	}
	if reused.proxyKey != firstKey {
		t.Fatalf("proxyKey = %q, want %q", reused.proxyKey, firstKey)
	}
	first.Release()
}

func TestHandleInvalidateDropsCachedExit(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{
		{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)},
		{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute)},
	})
	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	first, err := manager.Acquire(context.Background(), snapshot, "session-timeout")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstKey := first.proxyKey
	first.Invalidate()
	first.Release()

	second, err := manager.Acquire(context.Background(), snapshot, "session-timeout")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	t.Cleanup(second.Release)
	if second.proxyKey == firstKey {
		t.Fatalf("second Acquire reused timed-out proxy %q", firstKey)
	}
}

func TestManagerQualityRoutingPrefersRealRequestQuality(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{
		{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute), LatencyEWMA: 20 * time.Millisecond, LatencySamples: 8},
		{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute), RequestSuccessCount: 4, RequestLatencyEWMA: 180 * time.Millisecond, RequestLatencySamples: 4},
	})

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000, LatencyRoutingEnabled: true}
	handle, err := manager.Acquire(context.Background(), snapshot, "session-quality")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(handle.Release)
	if handle.proxyKey != (Proxy{Scheme: "http", Address: "10.0.0.2:8080"}).Key() {
		t.Fatalf("selected proxy = %q, want real-request quality exit", handle.proxyKey)
	}
}

func TestManagerRejectsMalformedSelectedProxyInsteadOfFallingBackDirect(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{{
		Scheme: "ftp", Address: "10.0.0.9:8080", ExpiresAt: now.Add(10 * time.Minute),
	}})

	_, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000, LatencyRoutingEnabled: true,
	}, "session-malformed-proxy")
	if err == nil {
		t.Fatal("Acquire with malformed selected proxy succeeded; proxy mode must not fall back to direct")
	}
	var proxyErr *Error
	if !errors.As(err, &proxyErr) || proxyErr.Reason() != ReasonTransportFailed {
		t.Fatalf("error = %T %v, want proxy transport failure", err, err)
	}
}

// TestHandleProxyKeyExposesExitIdentity proves the request path can read which
// pooled exit a handle dials, so failed/slow requests can be correlated with a
// specific proxy in logs (static-proxy mode reports empty).
func TestHandleProxyKeyExposesExitIdentity(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{{
		Scheme: "http", Address: "10.0.0.7:8080", ExpiresAt: now.Add(10 * time.Minute),
	}})

	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000,
	}, "session-proxykey")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(handle.Release)

	want := (Proxy{Scheme: "http", Address: "10.0.0.7:8080"}).Key()
	if got := handle.ProxyKey(); got != want {
		t.Fatalf("ProxyKey = %q, want %q", got, want)
	}
}

func TestHandleReportLatencyZeroRecordsSuccessfulRequest(t *testing.T) {
	now := time.Now()
	manager := newPoolManager(t, []Proxy{{
		Scheme: "http", Address: "10.0.0.8:8080", ExpiresAt: now.Add(10 * time.Minute),
	}})

	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{
		ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000,
	}, "session-request-success")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(handle.Release)

	handle.ReportLatency(0)
	proxies := manager.pool.List(time.Now())
	if len(proxies) != 1 {
		t.Fatalf("pool size = %d, want 1", len(proxies))
	}
	if proxies[0].RequestSuccessCount != 1 {
		t.Fatalf("request success count = %d, want 1", proxies[0].RequestSuccessCount)
	}
	if proxies[0].RequestLatencySamples != 0 {
		t.Fatalf("request latency samples = %d, want 0 for zero latency", proxies[0].RequestLatencySamples)
	}
}
