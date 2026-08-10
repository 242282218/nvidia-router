package xkproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
