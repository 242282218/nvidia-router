package xkproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingCollectorUpstream struct {
	t               *testing.T
	requestStarted  chan struct{}
	release         chan struct{}
	refreshDone     chan struct{}
	resourceClosed  chan struct{}
	closeOnce       sync.Once
	refreshDoneOnce sync.Once
}

func TestNewCollectorUsesExpectedQtyInUpstreamURL(t *testing.T) {
	collector := NewCollectorForTest(CollectorConfig{
		UpstreamURL:     "https://api.example.test/XApi?apikey=fixture&qty=1",
		UpstreamTimeout: time.Second,
		ExpectedQty:     7,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	client, ok := collector.upstream.(*UpstreamClient)
	if !ok {
		t.Fatalf("collector upstream type = %T, want *UpstreamClient", collector.upstream)
	}
	parsed, err := url.Parse(client.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if parsed.Query().Get("qty") != "7" {
		t.Fatalf("collector upstream qty = %q, want 7", parsed.Query().Get("qty"))
	}
	_ = collector.Close()
}

func (u *blockingCollectorUpstream) Fetch(ctx context.Context) ([]Proxy, time.Time, error) {
	close(u.requestStarted)
	select {
	case <-u.release:
		u.markRefreshDone()
		return nil, time.Now(), nil
	case <-ctx.Done():
		u.markRefreshDone()
		return nil, time.Time{}, ctx.Err()
	}
}

func (u *blockingCollectorUpstream) markRefreshDone() {
	u.refreshDoneOnce.Do(func() { close(u.refreshDone) })
}

func (u *blockingCollectorUpstream) Close() {
	u.closeOnce.Do(func() {
		select {
		case <-u.refreshDone:
		default:
			u.t.Errorf("upstream closed before manual refresh returned")
		}
		close(u.resourceClosed)
	})
}

// TestCollectorStartIdempotentAndCloseSafe proves Start is a one-shot
// operation: a second Start must not launch another run loop (both would
// mutate backoffLevel concurrently), and Start after Close must be a no-op
// that cannot hang Close's WaitGroup.
func TestCollectorStartIdempotentAndCloseSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	collector := NewCollectorForTest(CollectorConfig{
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
	refreshDone := make(chan struct{})
	upstream := &blockingCollectorUpstream{
		t:              t,
		requestStarted: requestStarted,
		release:        releaseUpstream,
		refreshDone:    refreshDone,
		resourceClosed: make(chan struct{}),
	}

	collector := NewCollectorForTest(CollectorConfig{
		UpstreamTimeout: time.Second,
		Interval:        time.Hour,
		ProxyTTL:        time.Minute,
		Concurrency:     1,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	collector.upstream = upstream

	refreshResult := make(chan error, 1)
	go func() {
		err := collector.Refresh(context.Background())
		refreshResult <- err
	}()
	<-requestStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- collector.Close() }()

	close(releaseUpstream)
	if err := <-refreshResult; err == nil {
		t.Fatal("Refresh unexpectedly succeeded with an empty upstream response")
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close after refresh: %v", err)
	}

	if err := collector.Refresh(context.Background()); err == nil || err.Error() != "proxy collector is closed" {
		t.Fatalf("Refresh after Close error = %v, want proxy collector is closed", err)
	}
}

func TestCollectorConcurrentCloseWaitsForResources(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	refreshDone := make(chan struct{})
	upstream := &blockingCollectorUpstream{
		t:              t,
		requestStarted: requestStarted,
		release:        releaseUpstream,
		refreshDone:    refreshDone,
		resourceClosed: make(chan struct{}),
	}
	collector := NewCollectorForTest(CollectorConfig{
		UpstreamTimeout: time.Second,
		Interval:        time.Hour,
		ProxyTTL:        time.Minute,
		Concurrency:     1,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	collector.upstream = upstream
	collector.closeCalls = make(chan struct{}, 2)

	refreshResult := make(chan error, 1)
	go func() {
		err := collector.Refresh(context.Background())
		refreshResult <- err
	}()
	<-requestStarted

	firstClose := make(chan error, 1)
	go func() { firstClose <- collector.Close() }()
	<-collector.closeCalls
	<-collector.done

	secondClose := make(chan error, 1)
	secondReturnedBeforeResourceClose := make(chan bool, 1)
	go func() {
		err := collector.Close()
		select {
		case <-upstream.resourceClosed:
			secondReturnedBeforeResourceClose <- false
		default:
			secondReturnedBeforeResourceClose <- true
		}
		secondClose <- err
	}()
	<-collector.closeCalls

	close(releaseUpstream)
	if err := <-refreshResult; err == nil {
		t.Fatal("Refresh unexpectedly succeeded with an empty upstream response")
	}
	if err := <-firstClose; err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := <-secondClose; err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if <-secondReturnedBeforeResourceClose {
		t.Fatal("second Close returned before resources were closed")
	}
	select {
	case <-collector.closeDone:
	default:
		t.Fatal("closeDone was not closed after resources were closed")
	}
}

// TestCollectorNormalizesZeroConcurrency proves a zero/negative Concurrency
// config cannot hang every validation goroutine on an empty semaphore.
func TestCollectorNormalizesZeroConcurrency(t *testing.T) {
	collector := NewCollectorForTest(CollectorConfig{
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

	collector := NewCollectorForTest(CollectorConfig{
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
	collector := NewCollectorForTest(CollectorConfig{
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
	collector := NewCollectorForTest(CollectorConfig{
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

// codeUpstream returns a fixed provider error code on every fetch.
type codeUpstream struct {
	code string
}

func (u *codeUpstream) Fetch(context.Context) ([]Proxy, time.Time, error) {
	return nil, time.Time{}, &ProviderError{Code: u.code, Message: "fixture"}
}

func (u *codeUpstream) Close() {}

// TestCollectorRateLimitCodeJumpsBackoff guards the ShouldBackOff wiring: a
// rate-limit (403/406) or account-level code must jump the fetch backoff
// straight to the deepest level so a throttled upstream is not polled at the
// base interval.
func TestCollectorRateLimitCodeJumpsBackoff(t *testing.T) {
	collector := NewCollectorForTest(CollectorConfig{
		UpstreamURL:     "https://fixture.invalid/",
		UpstreamTimeout: time.Second,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	collector.upstream = &codeUpstream{code: "403"}

	if collector.fetchSingleFlight(context.Background()) {
		t.Fatal("fetch returned true for a failing upstream")
	}
	if collector.backoffLevel != collectorMaxBackoffLevel {
		t.Fatalf("backoffLevel = %d, want %d after a rate-limit code", collector.backoffLevel, collectorMaxBackoffLevel)
	}
	minInterval := collector.interval << collectorMaxBackoffLevel
	got := collector.nextInterval(false)
	if got < minInterval || got > minInterval+minInterval/2 {
		t.Fatalf("interval = %v, want within [%v, %v] (jittered cap)", got, minInterval, minInterval+minInterval/2)
	}
	_ = collector.Close()
}

// TestCollectorPlainErrorBacksOffGradually proves ordinary fetch errors (no
// rate-limit/account code) still grow the backoff one level per failure instead
// of jumping to the cap.
func TestCollectorPlainErrorBacksOffGradually(t *testing.T) {
	collector := NewCollectorForTest(CollectorConfig{
		UpstreamURL:     "https://fixture.invalid/",
		UpstreamTimeout: time.Second,
	}, NewPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	collector.upstream = &codeUpstream{code: "201"} // request format invalid: not throttling

	if collector.fetchSingleFlight(context.Background()) {
		t.Fatal("fetch returned true for a failing upstream")
	}
	if collector.backoffLevel != 0 {
		t.Fatalf("backoffLevel = %d after first plain failure, want 0", collector.backoffLevel)
	}
	got := collector.nextInterval(false)
	if collector.backoffLevel != 1 {
		t.Fatalf("backoffLevel = %d after nextInterval, want 1", collector.backoffLevel)
	}
	minInterval := collector.interval << 1
	if got < minInterval || got > minInterval+minInterval/2 {
		t.Fatalf("interval = %v, want within [%v, %v] (jittered)", got, minInterval, minInterval+minInterval/2)
	}
	_ = collector.Close()
}
