package xkproxy

import (
	"math"
	"testing"
	"time"
)

// TestPoolGetWithLatencyPrefersMeasuredFastestExit proves latency-aware
// selection orders the rotation window by EWMA: the fastest measured exit is
// served first, slower measured exits follow, and unmeasured newcomers get
// exploration opportunities only after every measured exit has been served.
func TestPoolGetWithLatencyPrefersMeasuredFastestExit(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	slow := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute), LatencyEWMA: 500 * time.Millisecond, LatencySamples: 4}
	fast := Proxy{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute), LatencyEWMA: 80 * time.Millisecond, LatencySamples: 6}
	unmeasured := Proxy{Scheme: "http", Address: "10.0.0.3:8080", ExpiresAt: now.Add(10 * time.Minute)}
	pool.Replace([]Proxy{slow, fast, unmeasured})

	// First selection: the fastest measured exit, not the pool insertion order.
	picked, ok := pool.GetWithLatency(now)
	if !ok || picked.Address != fast.Address {
		t.Fatalf("first GetWithLatency = %+v, want the fastest exit", picked)
	}

	// The rotation window cycles fast -> slow -> unmeasured (exploration).
	second, _ := pool.GetWithLatency(now)
	if second.Address != slow.Address {
		t.Fatalf("second GetWithLatency = %+v, want the slower measured exit", second)
	}
	third, _ := pool.GetWithLatency(now)
	if third.Address != unmeasured.Address {
		t.Fatalf("third GetWithLatency = %+v, want the unmeasured exit (exploration)", third)
	}
}

// TestPoolGetKeepsLegacyRotationWithoutLatency proves the default selection
// path is untouched when latency-aware scheduling is off: rotation still
// applies and the 3x-slow demotion still works.
func TestPoolGetKeepsLegacyRotationWithoutLatency(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	first := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	second := Proxy{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute)}
	pool.Replace([]Proxy{first, second})

	pickedA, ok := pool.Get(now)
	if !ok || pickedA.Address == "" {
		t.Fatal("Get returned no proxy")
	}
	pickedB, _ := pool.Get(now)
	if pickedB.Address == pickedA.Address {
		t.Fatalf("two consecutive Gets returned the same exit %q; rotation must advance", pickedA.Address)
	}
}

// TestPoolStatusViewExposesQualityFields proves the admin view carries the new
// observability fields (ejection count, health fails, last failure, latency
// samples) so the UI can explain WHY an exit is degraded.
func TestPoolStatusViewExposesQualityFields(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute), LatencyEWMA: 120 * time.Millisecond, LatencySamples: 5}
	pool.Replace([]Proxy{proxy})
	pool.ReportHTTPFailure(proxy.Address, now, 429, policyWithHTTP(6))

	view := pool.List(now)
	if len(view) != 1 {
		t.Fatalf("List = %d proxies, want 1", len(view))
	}
	p := view[0]
	if p.EjectionCount != 0 || p.HealthFails != 0 {
		t.Fatalf("unexpected ejection/health state: ejection=%d health_fails=%d", p.EjectionCount, p.HealthFails)
	}
	if p.LastFailureAt.IsZero() {
		t.Fatal("LastFailureAt not recorded after an HTTP failure")
	}
	if p.LastHTTPFailureStatus != 429 || p.HTTPFailCount != 1 {
		t.Fatalf("HTTP failure fields = status %d count %d, want 429/1", p.LastHTTPFailureStatus, p.HTTPFailCount)
	}
	if p.LatencySamples != 5 {
		t.Fatalf("LatencySamples = %d, want 5", p.LatencySamples)
	}
}

func TestPoolReportSuccessRecordsRequestQuality(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	pool.Replace([]Proxy{proxy})

	pool.ReportSuccess(proxy.Address, now, 120*time.Millisecond, EjectionPolicy{})

	got := pool.List(now)[0]
	if got.RequestSuccessCount != 1 {
		t.Fatalf("RequestSuccessCount = %d, want 1", got.RequestSuccessCount)
	}
	if got.RequestLatencyEWMA != 120*time.Millisecond || got.RequestLatencySamples != 1 {
		t.Fatalf("request latency = %v/%d, want 120ms/1", got.RequestLatencyEWMA, got.RequestLatencySamples)
	}
}

func TestPoolReportRequestLatencyDoesNotCountSuccess(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	pool.Replace([]Proxy{proxy})

	pool.ReportRequestLatency(proxy.Address, now, 120*time.Millisecond, EjectionPolicy{})

	got := pool.List(now)[0]
	if got.RequestSuccessCount != 0 || got.RequestFailureCount != 0 {
		t.Fatalf("request outcome counts = %d/%d, want 0/0", got.RequestSuccessCount, got.RequestFailureCount)
	}
	if got.RequestLatencyEWMA != 120*time.Millisecond || got.RequestLatencySamples != 1 {
		t.Fatalf("request latency = %v/%d, want 120ms/1", got.RequestLatencyEWMA, got.RequestLatencySamples)
	}
}

func TestPoolReportFailureRecordsRequestFailure(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	pool.Replace([]Proxy{proxy})

	pool.ReportFailure(proxy.Address, now, EjectionPolicy{})

	got := pool.List(now)[0]
	if got.RequestFailureCount != 1 {
		t.Fatalf("RequestFailureCount = %d, want 1", got.RequestFailureCount)
	}
	if got.FailureCount != 1 || !got.LastFailureAt.Equal(now) {
		t.Fatalf("transport failure state = count %d at %v, want 1 at %v", got.FailureCount, got.LastFailureAt, now)
	}
}

func TestPoolReportRequestFailureAffectsQualityWithoutEjecting(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	pool.Replace([]Proxy{proxy})

	pool.ReportRequestFailure(proxy.Address, now)
	got := pool.List(now)[0]
	if got.RequestFailureCount != 1 || !got.LastFailureAt.Equal(now) {
		t.Fatalf("request failure state = count %d at %v, want 1 at %v", got.RequestFailureCount, got.LastFailureAt, now)
	}
	if got.FailureCount != 0 || got.HealthFails != 0 || got.EjectedUntil != (time.Time{}) {
		t.Fatalf("request body failure incorrectly changed transport state: %+v", got)
	}
}

func TestPoolQualityPrefersRealRequestSuccessOverProbeOnlyLatency(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	probeOnly := Proxy{
		Scheme:         "http",
		Address:        "10.0.0.1:8080",
		ExpiresAt:      now.Add(10 * time.Minute),
		LatencyEWMA:    20 * time.Millisecond,
		LatencySamples: 8,
	}
	reliable := Proxy{
		Scheme:                "http",
		Address:               "10.0.0.2:8080",
		ExpiresAt:             now.Add(10 * time.Minute),
		RequestSuccessCount:   4,
		RequestLatencyEWMA:    180 * time.Millisecond,
		RequestLatencySamples: 4,
	}
	pool.Replace([]Proxy{probeOnly, reliable})

	picked, ok := pool.GetWithQuality(now)
	if !ok || picked.Address != reliable.Address {
		t.Fatalf("GetWithQuality = %+v, want real-request reliable exit", picked)
	}
	if reliable.QualityScore() <= probeOnly.QualityScore() {
		t.Fatalf("reliable score = %d, probe-only score = %d; real request quality must win", reliable.QualityScore(), probeOnly.QualityScore())
	}
}

func TestPoolQualityDemotesHTTPFailureSignal(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	stable := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	throttled := Proxy{
		Scheme:        "http",
		Address:       "10.0.0.2:8080",
		ExpiresAt:     now.Add(10 * time.Minute),
		HTTPFailCount: 2,
	}
	pool.Replace([]Proxy{throttled, stable})

	picked, ok := pool.GetWithQuality(now)
	if !ok || picked.Address != stable.Address {
		t.Fatalf("GetWithQuality = %+v, want exit without HTTP failure signal", picked)
	}
}

func TestPoolQualityDemotesRecentRequestFailureStreak(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	stable := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute)}
	degraded := Proxy{
		Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute),
		RequestSuccessCount: 20, RequestLatencyEWMA: 40 * time.Millisecond,
	}
	pool.Replace([]Proxy{stable, degraded})
	for attempt := range 3 {
		pool.ReportRequestFailure(degraded.Address, now.Add(time.Duration(attempt)*time.Second))
	}

	picked, ok := pool.GetWithQuality(now.Add(3 * time.Second))
	if !ok || picked.Address != stable.Address {
		t.Fatalf("GetWithQuality = %+v, want stable exit after recent failure streak", picked)
	}
}

func TestPoolQualityExploresUnderSampledExitAtLowFrequency(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	best := Proxy{
		Scheme:              "http",
		Address:             "10.0.0.1:8080",
		ExpiresAt:           now.Add(10 * time.Minute),
		RequestSuccessCount: 20,
		RequestLatencyEWMA:  50 * time.Millisecond,
	}
	underSampled := Proxy{
		Scheme:    "http",
		Address:   "10.0.0.2:8080",
		ExpiresAt: now.Add(10 * time.Minute),
	}
	pool.Replace([]Proxy{best, underSampled})

	for attempt := 1; attempt < qualityExploreEvery; attempt++ {
		picked, ok := pool.GetWithQuality(now)
		if !ok || picked.Address != best.Address {
			t.Fatalf("selection %d = %+v, want best exit before exploration window", attempt, picked)
		}
	}
	picked, ok := pool.GetWithQuality(now)
	if !ok || picked.Address != underSampled.Address {
		t.Fatalf("exploration selection = %+v, want under-sampled exit", picked)
	}
}

// TestPoolQualityDoesNotExploreExitWhoseOnlyRequestSampleFailed locks in that
// exploration stays away from an under-sampled exit whose only real request
// evidence is a failure. A streak of zero otherwise makes it eligible: the
// request failed, and handing it more traffic on the exploration cadence would
// keep failing the client instead of learning anything new.
func TestPoolQualityDoesNotExploreExitWhoseOnlyRequestSampleFailed(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	best := Proxy{
		Scheme:              "http",
		Address:             "10.0.0.1:8080",
		ExpiresAt:           now.Add(10 * time.Minute),
		RequestSuccessCount: 20,
		RequestLatencyEWMA:  50 * time.Millisecond,
	}
	failedOnce := Proxy{
		Scheme:              "http",
		Address:             "10.0.0.2:8080",
		ExpiresAt:           now.Add(10 * time.Minute),
		RequestFailureCount: 1,
	}
	pool.Replace([]Proxy{best, failedOnce})
	pool.ReportRequestFailure(failedOnce.Address, now)

	for attempt := 1; attempt <= qualityExploreEvery*2; attempt++ {
		picked, ok := pool.GetWithQuality(now)
		if !ok || picked.Address != best.Address {
			t.Fatalf("selection %d = %+v, want best exit; failed-only under-sampled exit must not be explored", attempt, picked)
		}
	}
}

// TestPoolQualityDemotesRelativeSlowRequest proves a real-request exit several
// times slower than the pool's fastest real-request exit is demoted even when its
// success score is otherwise competitive — the pool must not keep routing to a
// path that is consistently far slower than a proven alternative.
func TestPoolQualityDemotesRelativeSlowRequest(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	fast := Proxy{
		Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute),
		RequestSuccessCount: 3, RequestLatencyEWMA: 100 * time.Millisecond, RequestLatencySamples: 3,
	}
	slow := Proxy{
		Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute),
		RequestSuccessCount: 3, RequestLatencyEWMA: 1200 * time.Millisecond, RequestLatencySamples: 3,
	}
	pool.Replace([]Proxy{fast, slow})

	picked, ok := pool.GetWithQuality(now)
	if !ok || picked.Address != fast.Address {
		t.Fatalf("GetWithQuality = %+v, want the fast real-request exit", picked)
	}
}

// TestPoolQualityDoesNotDemoteByProbeOnly proves probe latency never demotes a
// proven real-request exit: the two signals are different scales, and a probe-only
// exit must not outrank a reliable exit just because validation was faster.
func TestPoolQualityDoesNotDemoteByProbeOnly(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	probeFast := Proxy{
		Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute),
		LatencyEWMA: 20 * time.Millisecond, LatencySamples: 8,
	}
	reliable := Proxy{
		Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute),
		RequestSuccessCount: 4, RequestLatencyEWMA: 180 * time.Millisecond, RequestLatencySamples: 4,
	}
	pool.Replace([]Proxy{probeFast, reliable})

	picked, ok := pool.GetWithQuality(now)
	if !ok || picked.Address != reliable.Address {
		t.Fatalf("GetWithQuality = %+v, want the reliable real-request exit", picked)
	}
}

// TestPoolQualityColdStartDemotesSlowProbe proves an untested exit with a very
// slow collector probe is demoted before its first real request: probe latency is
// a prior for request latency, and demoting now saves the first request from
// paying the slow-tunnel penalty (measured on the 星空 pool: probe 4.1s → req 4.8s).
func TestPoolQualityColdStartDemotesSlowProbe(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	fastProbe := Proxy{Scheme: "http", Address: "10.0.0.1:8080", ExpiresAt: now.Add(10 * time.Minute), LatencyEWMA: 500 * time.Millisecond}
	slowProbe := Proxy{Scheme: "http", Address: "10.0.0.2:8080", ExpiresAt: now.Add(10 * time.Minute), LatencyEWMA: 4 * time.Second}
	pool.Replace([]Proxy{fastProbe, slowProbe})

	if fastProbe.QualityScore() <= slowProbe.QualityScore() {
		t.Fatalf("fast-probe score = %d, slow-probe score = %d; slow probe must be demoted at cold start", fastProbe.QualityScore(), slowProbe.QualityScore())
	}
	picked, ok := pool.GetWithQuality(now)
	if !ok || picked.Address != fastProbe.Address {
		t.Fatalf("GetWithQuality = %+v, want the fast-probe exit", picked)
	}
}

// TestPoolQualityDoesNotDemoteByProbeAfterRequestSample proves once a real-request
// sample exists, request latency wins and probe latency no longer demotes: the two
// scales must not be mixed for an exit that already has real evidence.
func TestPoolQualityDoesNotDemoteByProbeAfterRequestSample(t *testing.T) {
	pool := NewPool()
	now := time.Now()
	proven := Proxy{
		Scheme: "http", Address: "10.0.0.3:8080", ExpiresAt: now.Add(10 * time.Minute),
		RequestSuccessCount: 3, RequestLatencyEWMA: 150 * time.Millisecond, RequestLatencySamples: 3,
		LatencyEWMA: 4 * time.Second, // probe slow, but real request is fast
	}
	pool.Replace([]Proxy{proven})

	if proven.QualityScore() != 50+int(math.Round((1.0-0.5)*60*math.Min(3, 10)/10))-3 {
		t.Fatalf("proven score = %d, want request-latency-based score (probe must not demote)", proven.QualityScore())
	}
}
