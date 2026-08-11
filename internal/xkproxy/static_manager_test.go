package xkproxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func TestManagerUsesProxyPoolBasicAuthAndReusesTransport(t *testing.T) {
	const authHeader = "Basic cHJveHk6cHJveHktc2VjcmV0"
	var proxyRequests int
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests++
		if got := request.Header.Get("Proxy-Authorization"); got != authHeader {
			t.Errorf("Proxy-Authorization = %q, want %q", got, authHeader)
		}
		writer.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	manager, err := New(proxyURL, "proxy-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(manager.Close)

	first, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}, "")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}, "")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if first.Transport() != second.Transport() {
		t.Fatal("same timeout snapshot did not reuse Transport")
	}
	request, err := http.NewRequest(http.MethodGet, "http://target.example.test/health", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	client := &http.Client{Transport: first.Transport()}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	_ = response.Body.Close()
	if proxyRequests != 1 {
		t.Fatalf("proxy requests = %d, want 1", proxyRequests)
	}
	first.Release()
	second.Release()
}

func TestManagerRejectsMissingProxyAuthKey(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy-pool:8080")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	_, err = New(proxyURL, "", http.DefaultTransport.(*http.Transport), nil)
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("New error = %v, want authentication error", err)
	}
}

func TestManagerBoundsTransportCacheWithLRU(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy-pool:8080")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	manager, err := New(proxyURL, "proxy-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(manager.Close)

	firstKey := transportKey{connectTimeoutMS: 1, firstByteTimeoutMS: 1}
	secondKey := transportKey{connectTimeoutMS: 2, firstByteTimeoutMS: 2}
	first, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1, FirstByteTimeoutMS: 1}, "")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if _, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 2, FirstByteTimeoutMS: 2}, ""); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if _, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1, FirstByteTimeoutMS: 1}, ""); err != nil {
		t.Fatalf("touch first transport: %v", err)
	}
	for timeout := 3; timeout <= maxCachedTransports+1; timeout++ {
		if _, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: timeout, FirstByteTimeoutMS: timeout}, ""); err != nil {
			t.Fatalf("Acquire timeout %d: %v", timeout, err)
		}
	}
	if len(manager.transports) != maxCachedTransports {
		t.Fatalf("transport cache size = %d, want %d", len(manager.transports), maxCachedTransports)
	}
	if manager.transports[firstKey] == nil {
		t.Fatal("recently used first transport was evicted")
	}
	if manager.transports[secondKey] != nil {
		t.Fatal("least recently used second transport was not evicted")
	}
	if first.Transport() == nil {
		t.Fatal("first handle lost its transport after cache eviction")
	}

	manager.Close()
	if manager.Enabled() {
		t.Fatal("closed manager remains enabled")
	}
	if _, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{}, ""); err == nil {
		t.Fatal("Acquire succeeded after Close")
	}
}

// TestManagerIsolatesSessionsFromTransportReuse proves the session label is part of
// the transport identity: two requests with the same session and timeout share a
// transport, different sessions never do even with identical timeouts.
func TestManagerIsolatesSessionsFromTransportReuse(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy-pool:8080")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	manager, err := New(proxyURL, "proxy-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(manager.Close)

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	first, err := manager.Acquire(context.Background(), snapshot, "session-a")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, err := manager.Acquire(context.Background(), snapshot, "session-a")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if first.Transport() != second.Transport() {
		t.Fatal("same session + timeout did not reuse Transport")
	}

	other, err := manager.Acquire(context.Background(), snapshot, "session-b")
	if err != nil {
		t.Fatalf("other-session Acquire: %v", err)
	}
	if other.Transport() == first.Transport() {
		t.Fatal("different sessions shared a Transport; CONNECT tunnels would cross keys")
	}
	unkeyed, err := manager.Acquire(context.Background(), snapshot, "")
	if err != nil {
		t.Fatalf("unkeyed Acquire: %v", err)
	}
	if unkeyed.Transport() == first.Transport() || unkeyed.Transport() == other.Transport() {
		t.Fatal("unkeyed request shared a keyed Transport")
	}
	first.Release()
	second.Release()
	other.Release()
	unkeyed.Release()
}

// TestManagerSetsStickyHeaderOnOuterConnect proves the manager wires the session
// label onto the proxy CONNECT via GetProxyConnectHeader, where the pool can read it,
// rather than leaving it to leak onto the target request.
func TestManagerSetsStickyHeaderOnOuterConnect(t *testing.T) {
	const wantHeader = "X-XK-Session"
	var gotSession string
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			http.Error(writer, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		gotSession = request.Header.Get(wantHeader)
		// Simulate the pool accepting the tunnel; the client only needs to see the
		// 200 to proceed, and the connection is closed immediately after.
		conn, _, err := hijackProxyConn(writer)
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		_ = conn.Close()
	}))
	t.Cleanup(proxy.Close)

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	manager, err := New(proxyURL, "proxy-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(manager.Close)

	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}, "session-sticky")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(handle.Release)

	// An HTTPS target forces the Transport to open a CONNECT tunnel, which is the
	// only path that consults GetProxyConnectHeader. The tunnel then immediately
	// closes so the TLS handshake to the fake target fails; that is fine — the
	// header was already captured on the CONNECT itself.
	request, err := http.NewRequest(http.MethodGet, "https://target.example.test/health", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	client := &http.Client{Transport: handle.Transport()}
	_, _ = client.Do(request) //nolint:bodyclose // response body is never opened on the failed handshake
	if gotSession != "session-sticky" {
		t.Fatalf("CONNECT %s = %q, want %q", wantHeader, gotSession, "session-sticky")
	}
}

func hijackProxyConn(writer http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("proxy writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func TestManagerCachedTransportKeepsProxyIdentityAndSurvivesEmptyPool(t *testing.T) {
	// Audit P1-2 / P2-2: a cache hit must reuse the transport AND report the
	// proxy that transport was actually built against, and it must keep working
	// even when the pool momentarily has no healthy proxy (TTL expiry between
	// fetches). Regression: proxyKey used to be the rotation cursor's current
	// value, so a reused transport reported a different proxy than it dialed,
	// and an empty pool rejected even a healthy cached connection.
	base := http.DefaultTransport.(*http.Transport)
	manager, err := NewWithPool(CollectorConfig{UpstreamTimeout: time.Second}, "proxy-secret", base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewWithPool: %v", err)
	}
	t.Cleanup(manager.Close)

	first := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: time.Now().Add(10 * time.Minute)}
	second := Proxy{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: time.Now().Add(10 * time.Minute)}
	manager.pool.Replace([]Proxy{first, second})

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	acquired, err := manager.Acquire(context.Background(), snapshot, "")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstKey := acquired.proxyKey

	// Force the rotation cursor past the first proxy so a naive Get would return
	// the second one; a cache hit must still report the transport's bound proxy.
	manager.pool.selectionCursor.Add(1)

	cached, err := manager.Acquire(context.Background(), snapshot, "")
	if err != nil {
		t.Fatalf("cached Acquire: %v", err)
	}
	if cached.Transport() != acquired.Transport() {
		t.Fatal("same snapshot did not reuse cached transport")
	}
	if cached.proxyKey != firstKey {
		t.Fatalf("cached proxyKey = %q, want %q (transport is bound to the first proxy)", cached.proxyKey, firstKey)
	}

	// Empty the pool: a healthy cached transport must still be acquirable.
	manager.pool.Clear()
	reused, err := manager.Acquire(context.Background(), snapshot, "")
	if err != nil {
		t.Fatalf("Acquire with empty pool and cached transport: %v", err)
	}
	if reused.Transport() != acquired.Transport() {
		t.Fatal("empty-pool Acquire did not reuse the cached transport")
	}
	if reused.proxyKey != firstKey {
		t.Fatalf("empty-pool proxyKey = %q, want %q", reused.proxyKey, firstKey)
	}

	acquired.Release()
	cached.Release()
	reused.Release()
}

func TestManagerRebuildsTransportWhenCachedProxyEjected(t *testing.T) {
	// Audit H3: a cached transport bound to a proxy that gets ejected must be
	// rebuilt against a fresh pool proxy on the next Acquire, instead of keeping
	// traffic pinned to a dead exit until a connection-level failure.
	base := http.DefaultTransport.(*http.Transport)
	manager, err := NewWithPool(CollectorConfig{UpstreamTimeout: time.Second}, "proxy-secret", base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewWithPool: %v", err)
	}
	t.Cleanup(manager.Close)

	now := time.Now()
	first := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	manager.pool.Replace([]Proxy{first})

	snapshot := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	acquired, err := manager.Acquire(context.Background(), snapshot, "")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstKey := acquired.proxyKey
	acquired.Release()

	// Eject the first proxy and replace the pool with a second one.
	policy := EjectionPolicy{FailureLimit: 1, BaseDuration: time.Second, MaxDuration: time.Second, MaxEjections: 1}
	manager.pool.ReportFailure(firstKey, time.Now(), policy)
	manager.pool.ReportFailure(firstKey, time.Now(), policy)
	manager.pool.Replace([]Proxy{{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: time.Now().Add(10 * time.Minute)}})

	rebuilt, err := manager.Acquire(context.Background(), snapshot, "")
	if err != nil {
		t.Fatalf("Acquire after ejection: %v", err)
	}
	t.Cleanup(rebuilt.Release)
	if rebuilt.proxyKey == firstKey {
		t.Fatalf("rebuilt proxyKey = %q, want a different proxy (cached dead proxy not rebuilt)", rebuilt.proxyKey)
	}
	if rebuilt.Transport() == acquired.Transport() {
		t.Fatal("Acquire reused the transport bound to the ejected proxy")
	}
}
