package xkproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCollectorStartIdempotentAndCloseSafe proves Start is a one-shot
// operation: a second Start must not launch another run loop (both would
// mutate backoffLevel concurrently), and Start after Close must be a no-op
// that cannot hang Close's WaitGroup.
func TestCollectorStartIdempotentAndCloseSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	collector := NewCollector(CollectorConfig{
		UpstreamURL:     server.URL,
		UpstreamTimeout: 200 * time.Millisecond,
		Interval:        time.Hour,
		ProxyTTL:        time.Minute,
		Concurrency:     2,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	collector.Start(ctx)
	collector.Start(ctx) // must be a no-op

	if err := collector.Close(); err != nil {
		t.Fatalf("Close after double Start: %v", err)
	}
	// Start after Close must not panic and must not add goroutines.
	collector.Start(ctx)
	if err := collector.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	cancel()
}

func TestCollectorCloseWaitsForManualRefresh(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	release := func() {
		select {
		case <-releaseUpstream:
		default:
			close(releaseUpstream)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseUpstream
	}))
	defer upstream.Close()
	defer release()

	collector := NewCollector(CollectorConfig{
		UpstreamURL:     upstream.URL,
		UpstreamTimeout: time.Second,
		Interval:        time.Hour,
		ProxyTTL:        time.Minute,
		Concurrency:     1,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	refreshDone := make(chan error, 1)
	go func() { refreshDone <- collector.Refresh(context.Background()) }()
	<-requestStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- collector.Close() }()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the manual refresh exited: %v", err)
	case <-time.After(time.Second):
	}

	release()
	if err := <-refreshDone; err == nil {
		t.Fatal("Refresh unexpectedly succeeded with an empty upstream response")
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close after refresh: %v", err)
	}

	if err := collector.Refresh(context.Background()); err == nil || err.Error() != "proxy collector is closed" {
		t.Fatalf("Refresh after Close error = %v, want proxy collector is closed", err)
	}
}

// TestCollectorNormalizesZeroConcurrency proves a zero/negative Concurrency
// config cannot hang every validation goroutine on an empty semaphore.
func TestCollectorNormalizesZeroConcurrency(t *testing.T) {
	collector := NewCollector(CollectorConfig{
		Interval: time.Hour,
		ProxyTTL: time.Minute,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if collector.concurrency != 1 {
		t.Fatalf("normalized concurrency = %d, want 1", collector.concurrency)
	}
}

// TestCollectorValidationAllFailedExtendsLiveProxies proves that a fetch which
// succeeds at the provider but yields only unusable exits still extends live
// proxies' TTL. A bad-exit patch from the provider must not drain the pool and
// turn a provider quality dip into a request outage (audit H6).
func TestCollectorValidationAllFailedExtendsLiveProxies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "127.0.0.1:1\n")
	}))
	defer upstream.Close()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	now := time.Now()
	pool := NewPool()
	pool.Replace([]Proxy{{
		Scheme: "http", Address: "203.0.113.7:8080",
		ExpiresAt:   now.Add(30 * time.Second),
		LatencyEWMA: 100 * time.Millisecond,
		ValidatedAt: now,
	}})

	collector := NewCollector(CollectorConfig{
		UpstreamURL:       upstream.URL,
		UpstreamTimeout:   500 * time.Millisecond,
		ValidationURL:     closedURL,
		ValidationStatus:  200,
		ValidationTimeout: 500 * time.Millisecond,
		Interval:          time.Hour,
		ProxyTTL:          time.Minute,
		Concurrency:       2,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if collector.fetch(context.Background()) {
		t.Fatal("fetch returned true: validation failed for every exit, backoff should not reset")
	}
	proxies := pool.List(time.Now())
	if len(proxies) != 1 {
		t.Fatalf("live proxies = %d, want 1 (grace must retain last-known-good exit)", len(proxies))
	}
	// proxyTTL/2 grace = 30s, so the 30s-remaining exit is extended to ~60s.
	if !proxies[0].ExpiresAt.After(now.Add(30 * time.Second)) {
		t.Fatalf("live proxy ExpiresAt = %v, want extended beyond %v", proxies[0].ExpiresAt, now.Add(30*time.Second))
	}
}

func TestCollectorValidationAllFailedKeepsLastSuccessEmpty(t *testing.T) {
	// Upstream returns one proxy that points at a closed port: fetch succeeds,
	// validation must fail.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "127.0.0.1:1\n")
	}))
	defer upstream.Close()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close() // validation target unreachable

	pool := NewPool()
	collector := NewCollector(CollectorConfig{
		UpstreamURL:       upstream.URL,
		UpstreamTimeout:   500 * time.Millisecond,
		ValidationURL:     closedURL,
		ValidationStatus:  200,
		ValidationTimeout: 500 * time.Millisecond,
		Interval:          time.Hour,
		ProxyTTL:          time.Minute,
		Concurrency:       2,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if collector.fetch(context.Background()) {
		t.Fatal("fetch returned true: validation failed for every exit, backoff should not reset")
	}
	fetchedAt, successAt, code := collector.LastFetchResult()
	if fetchedAt.IsZero() {
		t.Fatal("lastFetchAt must be set when the upstream answered")
	}
	if !successAt.IsZero() {
		t.Fatal("lastSuccessAt must stay empty when every exit failed validation")
	}
	if code != "" {
		t.Fatalf("last error code = %q, want empty (upstream fetch succeeded)", code)
	}
	if size := pool.Size(time.Now()); size != 0 {
		t.Fatalf("pool size = %d, want 0 (no validated exits)", size)
	}
}

// TestCollectorRefetchesAfterValidationAllFailed proves a fetch that receives a
// dead exit re-fetches within the same call instead of giving up: proxy quality
// is random per lease, so a later lease usually lands a usable exit.
func TestCollectorRefetchesAfterValidationAllFailed(t *testing.T) {
	// The fake proxy answers any forwarded request with 200, so a validation
	// target reached through it passes.
	fakeProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeProxy.Close()
	proxyAddr := strings.TrimPrefix(fakeProxy.URL, "http://")

	valid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer valid.Close()

	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		if upstreamCalls == 1 {
			_, _ = io.WriteString(w, "127.0.0.1:1\n") // dead exit
			return
		}
		_, _ = io.WriteString(w, proxyAddr+"\n")
	}))
	defer upstream.Close()

	pool := NewPool()
	collector := NewCollector(CollectorConfig{
		UpstreamURL:       upstream.URL,
		UpstreamTimeout:   500 * time.Millisecond,
		ValidationURL:     valid.URL,
		ValidationStatus:  200,
		ValidationTimeout: 500 * time.Millisecond,
		Interval:          time.Hour,
		ProxyTTL:          time.Minute,
		Concurrency:       2,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !collector.fetch(context.Background()) {
		t.Fatal("fetch returned false: the second lease validated")
	}
	if upstreamCalls < 2 {
		t.Fatalf("upstream calls = %d, want >= 2 (dead first lease should be re-fetched)", upstreamCalls)
	}
	if size := pool.Size(time.Now()); size != 1 {
		t.Fatalf("pool size = %d, want 1 validated exit", size)
	}
}
