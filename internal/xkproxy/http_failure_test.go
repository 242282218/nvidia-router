package xkproxy

import (
	"testing"
	"time"
)

// policyWithHTTP returns an ejection policy exercising the HTTP-failure path
// with a small failure limit so tests stay short.
func policyWithHTTP(httpLimit int) EjectionPolicy {
	return EjectionPolicy{
		FailureLimit:     3,
		BaseDuration:     10 * time.Second,
		MaxDuration:      60 * time.Second,
		MaxEjections:     3,
		HTTPFailureLimit: httpLimit,
	}
}

func httpTestProxy(address string, now time.Time) Proxy {
	return Proxy{
		Scheme:    "http",
		Address:   address,
		FetchedAt: now,
		ExpiresAt: now.Add(10 * time.Minute),
	}
}

// TestHTTPFailureCountedBelowLimit proves an HTTP failure below the limit is
// only counted, never ejected, while the pool keeps serving the exit.
func TestHTTPFailureCountedBelowLimit(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := httpTestProxy("10.0.0.1:8080", now)
	pool.Merge(now, []Proxy{proxy})

	// Gate-open evidence: the same exit served a real 2xx a moment ago.
	pool.ReportSuccess(proxy.Address, now, 50*time.Millisecond, policyWithHTTP(3))

	for attempt := 0; attempt < 2; attempt++ {
		outcome := pool.ReportHTTPFailure(proxy.Address, now, policyWithHTTP(3))
		if outcome != OutcomeCounted {
			t.Fatalf("attempt %d: outcome = %v, want OutcomeCounted", attempt, outcome)
		}
	}
	if size := pool.Size(now); size != 1 {
		t.Fatalf("pool size = %d, want 1 (counted failures must not isolate)", size)
	}
	if list := pool.List(now); len(list) != 1 || list[0].HTTPFailCount != 2 {
		t.Fatalf("HTTPFailCount = %d, want 2", list[0].HTTPFailCount)
	}
}

// TestHTTPFailureEjectsOnlyWhilePoolWorks proves the core fix (audit H8): a
// proxy that keeps returning 429/5xx while other exits serve 2xx is isolated.
func TestHTTPFailureEjectsOnlyWhilePoolWorks(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	good := httpTestProxy("10.0.0.1:8080", now)
	bad := httpTestProxy("10.0.0.2:8080", now)
	pool.Merge(now, []Proxy{good, bad})

	// The good exit serves a real 2xx: pool-level "it is working" evidence.
	pool.ReportSuccess(good.Address, now, 40*time.Millisecond, policyWithHTTP(3))

	for attempt := 0; attempt < 2; attempt++ {
		if outcome := pool.ReportHTTPFailure(bad.Address, now, policyWithHTTP(3)); outcome != OutcomeCounted {
			t.Fatalf("counted attempt %d: outcome = %v, want OutcomeCounted", attempt, outcome)
		}
	}
	if size := pool.Size(now); size != 2 {
		t.Fatalf("pool size = %d, want 2 before the limit is reached", size)
	}

	if outcome := pool.ReportHTTPFailure(bad.Address, now, policyWithHTTP(3)); outcome != OutcomeEjected {
		t.Fatalf("third failure: outcome = %v, want OutcomeEjected", outcome)
	}
	if size := pool.Size(now); size != 1 {
		t.Fatalf("pool size = %d, want 1 (bad exit isolated, good exit still available)", size)
	}

	// The isolated exit must not be selected while ejected.
	if picked, ok := pool.Get(now); !ok || picked.Key() != good.Key() {
		t.Fatalf("Get after ejection = %+v, want the good proxy", picked)
	}

	// A real 2xx through the isolated exit proves it recovered: isolation is
	// cleared and the HTTP-failure pattern resets.
	pool.ReportSuccess(bad.Address, now, 60*time.Millisecond, policyWithHTTP(3))
	if size := pool.Size(now.Add(1 * time.Second)); size != 2 {
		t.Fatalf("pool size after recovery = %d, want 2", size)
	}
	for _, proxy := range pool.List(now.Add(1 * time.Second)) {
		if proxy.Address == bad.Address && proxy.HTTPFailCount != 0 {
			t.Fatalf("recovered proxy HTTPFailCount = %d, want 0", proxy.HTTPFailCount)
		}
	}
}

// TestHTTPFailureSystemicThrottleDoesNotEject proves the majority gate (audit
// H8): when NO exit has served a recent 2xx, repeated 429/5xx are a key-level
// or global condition and must not blame-and-eject the pool.
func TestHTTPFailureSystemicThrottleDoesNotEject(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	first := httpTestProxy("10.0.0.1:8080", now)
	second := httpTestProxy("10.0.0.2:8080", now)
	pool.Merge(now, []Proxy{first, second})

	// Merge sets LastSuccessAt from validation, but the isolation gate must only
	// trust real request 2xx (LastRequestSuccessAt), which neither proxy has. A
	// key-wide throttle hits every exit, so report on both.
	for attempt := 0; attempt < 10; attempt++ {
		outcome := pool.ReportHTTPFailure(first.Address, now, policyWithHTTP(3))
		if outcome != OutcomeCounted {
			t.Fatalf("systemic failure %d (first): outcome = %v, want OutcomeCounted", attempt, outcome)
		}
		outcome = pool.ReportHTTPFailure(second.Address, now, policyWithHTTP(3))
		if outcome != OutcomeCounted {
			t.Fatalf("systemic failure %d (second): outcome = %v, want OutcomeCounted", attempt, outcome)
		}
	}
	if size := pool.Size(now); size != 2 {
		t.Fatalf("pool size = %d, want 2 (key-wide throttle must not empty the pool)", size)
	}
	// The pattern is observed (saturated at the limit) but never acted on while
	// the whole pool is failing.
	for _, proxy := range pool.List(now) {
		if proxy.HTTPFailCount != 3 {
			t.Fatalf("proxy %s HTTPFailCount = %d, want 3 (saturated at the limit)", proxy.Address, proxy.HTTPFailCount)
		}
	}

	// Once the upstream recovers and ANY exit serves a 2xx, the gate opens and
	// the saturated pattern immediately leads to isolation.
	pool.ReportSuccess(second.Address, now, 40*time.Millisecond, policyWithHTTP(3))
	if outcome := pool.ReportHTTPFailure(first.Address, now.Add(time.Second), policyWithHTTP(3)); outcome != OutcomeEjected {
		t.Fatalf("failure after pool recovery: outcome = %v, want OutcomeEjected", outcome)
	}
}

// TestHTTPFailureDoesNotFeedLatencyEWMA proves a fast rejection never makes a
// failing exit look fast (a 429 answered instantly must not rank the exit
// ahead of genuinely fast, successful exits).
func TestHTTPFailureDoesNotFeedLatencyEWMA(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := httpTestProxy("10.0.0.1:8080", now)
	proxy.LatencyEWMA = 500 * time.Millisecond
	pool.Merge(now, []Proxy{proxy})

	pool.ReportHTTPFailure(proxy.Address, now, policyWithHTTP(6))
	list := pool.List(now)
	if len(list) != 1 || list[0].LatencyEWMA != 500*time.Millisecond {
		t.Fatalf("LatencyEWMA changed on HTTP failure: %v, want unchanged 500ms", list[0].LatencyEWMA)
	}
}

// TestPoolGraceExtendsLiveProxiesDuringUpstreamOutage proves last-known-good
// exits survive a short provider outage (audit H6): a live proxy's expiry is
// pushed out by the grace window on fetch failure, while the extension is
// capped so a stale exit is not served forever.
func TestPoolGraceExtendsLiveProxiesDuringUpstreamOutage(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	live := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(2 * time.Minute)}
	// A second proxy whose TTL already lapsed must NOT be resurrected by grace.
	expired := Proxy{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(-time.Second)}
	pool.Replace([]Proxy{live, expired})

	extended := pool.Grace(now, time.Minute, 3*time.Minute)
	if extended != 1 {
		t.Fatalf("Grace extended = %d, want 1 (only the live proxy)", extended)
	}
	if size := pool.LiveSize(now); size != 1 {
		t.Fatalf("LiveSize = %d, want 1 (expired proxy not resurrected)", size)
	}
	list := pool.List(now)
	if len(list) != 1 || list[0].Address != "10.0.0.1:8080" {
		t.Fatalf("live proxies after Grace = %+v", list)
	}
	if !list[0].ExpiresAt.After(now.Add(2*time.Minute)) {
		t.Fatalf("ExpiresAt not extended: %v, want past the original TTL", list[0].ExpiresAt)
	}

	// The cap bounds staleness: many grace calls cannot push expiry past now+max.
	for range 10 {
		pool.Grace(now, time.Minute, 3*time.Minute)
	}
	for _, proxy := range pool.List(now) {
		if proxy.ExpiresAt.After(now.Add(3*time.Minute)) {
			t.Fatalf("ExpiresAt %v exceeds the max lifetime cap", proxy.ExpiresAt)
		}
	}
}

// TestPoolGraceNoopOnInvalidWindows proves Grace with zero windows is a no-op
// rather than corrupting the pool.
func TestPoolGraceNoopOnInvalidWindows(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(2 * time.Minute)}
	pool.Replace([]Proxy{proxy})

	if extended := pool.Grace(now, 0, time.Minute); extended != 0 {
		t.Fatalf("Grace with zero grace extended = %d, want 0", extended)
	}
	if extended := pool.Grace(now, time.Minute, 0); extended != 0 {
		t.Fatalf("Grace with zero maxLifetime extended = %d, want 0", extended)
	}
}
