package nvidiakey

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/keystate"
)

// stubHealthRepository implements healthRepository with a fixed candidate list
// so the sweep can be driven deterministically without a real DB.
type stubHealthRepository struct {
	candidates []keystate.KeySnapshot
	err        error
	called     atomic.Int64
}

func (s *stubHealthRepository) ListKeysForHealthCheck(_ context.Context) ([]keystate.KeySnapshot, error) {
	s.called.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.candidates, nil
}

// stubWriter captures MarkSuccess calls; failures can be injected via failOn.
type stubWriter struct {
	mu        sync.Mutex
	recovered []int64
	failOn    int64
	failErr   error
}

func (w *stubWriter) MarkSuccess(_ context.Context, keyID int64) (keystate.KeySnapshot, error) {
	if w.failErr != nil && keyID == w.failOn {
		return keystate.KeySnapshot{}, w.failErr
	}
	w.mu.Lock()
	w.recovered = append(w.recovered, keyID)
	w.mu.Unlock()
	return keystate.KeySnapshot{ID: keyID}, nil
}

func (w *stubWriter) recoveries() []int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int64, len(w.recovered))
	copy(out, w.recovered)
	return out
}

func discardHealthLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestHealthChecker(t *testing.T, repo healthRepository) *HealthChecker {
	t.Helper()
	checker := NewHealthChecker(repo, nil /* RealClock fallback */, HealthCheckerOptions{
		Logger: discardHealthLogger(),
	})
	return checker
}

func TestHealthCheckerSweepRecoversValidKeysOnly(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{
		{ID: 10, CooldownLevel: 2},
		{ID: 11, ConsecutiveFailures: 1},
		{ID: 12, CooldownLevel: 1},
	}}
	writer := &stubWriter{}
	checker := newTestHealthChecker(t, repo)
	checker.WireWriter(writer)
	// Probe: only IDs 10 and 12 are valid this sweep.
	results := map[int64]ProbeResult{
		10: {Recovered: true, Category: "valid"},
		11: {Category: "temporarily_unavailable"},
		12: {Recovered: true, Category: "valid"},
	}
	checker.WireProbe(func(_ context.Context, id int64) ProbeResult { return results[id] })

	if err := checker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	got := writer.recoveries()
	want := []int64{10, 12} // order follows candidate order; concurrency is bounded but recovery writes are independent
	if len(got) != len(want) {
		t.Fatalf("recoveries = %v, want %v", got, want)
	}
	seen := map[int64]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[10] || !seen[12] {
		t.Fatalf("recoveries missing expected ids: %v", got)
	}
	if seen[11] {
		t.Fatalf("non-recovered key 11 was marked success: %v", got)
	}
}

func TestHealthCheckerSweepSkipsWhenNoCandidates(t *testing.T) {
	repo := &stubHealthRepository{candidates: nil}
	writer := &stubWriter{}
	checker := newTestHealthChecker(t, repo)
	checker.WireWriter(writer)
	probeCalls := atomic.Int64{}
	checker.WireProbe(func(ctx context.Context, id int64) ProbeResult {
		probeCalls.Add(1)
		return ProbeResult{Recovered: true, Category: "valid"}
	})

	if err := checker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if probeCalls.Load() != 0 {
		t.Fatalf("probe calls = %d, want 0 on empty candidate list", probeCalls.Load())
	}
	if calls := repo.called.Load(); calls != 1 {
		t.Fatalf("repository calls = %d, want 1", calls)
	}
}

func TestHealthCheckerSweepReturnsListError(t *testing.T) {
	repo := &stubHealthRepository{err: errors.New("db offline")}
	checker := newTestHealthChecker(t, repo)
	checker.WireWriter(&stubWriter{})
	checker.WireProbe(func(context.Context, int64) ProbeResult { return ProbeResult{Category: "valid"} })

	if err := checker.Sweep(context.Background()); err == nil {
		t.Fatal("Sweep error = nil, want list error")
	}
}

func TestHealthCheckerSweepIsNoOpWhenNotWired(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{{ID: 1, CooldownLevel: 1}}}
	checker := newTestHealthChecker(t, repo)
	// No WireProbe / WireWriter — sweep must skip rather than panic.
	if err := checker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if calls := repo.called.Load(); calls != 0 {
		t.Fatalf("repository calls = %d, want 0 when not wired", calls)
	}
}

func TestHealthCheckerSweepLogsMarkSuccessFailureAndContinues(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{
		{ID: 1, CooldownLevel: 1},
		{ID: 2, CooldownLevel: 1},
	}}
	writer := &stubWriter{failOn: 1, failErr: errors.New("write boom")}
	checker := newTestHealthChecker(t, repo)
	checker.WireWriter(writer)
	checker.WireProbe(func(_ context.Context, _ int64) ProbeResult {
		return ProbeResult{Recovered: true, Category: "valid"}
	})

	if err := checker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// key 1 should fail to recover; key 2 should succeed. Order is not
	// guaranteed because of concurrency, so check membership only.
	got := writer.recoveries()
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("recoveries = %v, want [2]", got)
	}
}

func TestHealthCheckerRunSweepsUntilContextCancel(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{{ID: 1, CooldownLevel: 1}}}
	writer := &stubWriter{}
	checker := NewHealthChecker(repo, nil, HealthCheckerOptions{
		Interval: time.Hour, // not actually used because Wait is overridden
		Wait: func(ctx context.Context, _ time.Duration) bool {
			<-ctx.Done()
			return false
		},
		Logger: discardHealthLogger(),
	})
	checker.WireWriter(writer)
	checker.WireProbe(func(context.Context, int64) ProbeResult { return ProbeResult{Recovered: true, Category: "valid"} })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
	// Wait short-circuited to false immediately, so Sweep was never invoked.
	if calls := repo.called.Load(); calls != 0 {
		t.Fatalf("repository calls = %d, want 0 when Wait immediately cancels", calls)
	}
}

func TestHealthCheckerRunSweepsWhenWaitElapses(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{{ID: 7, CooldownLevel: 1}}}
	writer := &stubWriter{}
	waitCalls := 0
	checker := NewHealthChecker(repo, nil, HealthCheckerOptions{
		Interval: time.Hour,
		Wait: func(ctx context.Context, _ time.Duration) bool {
			waitCalls++
			if waitCalls == 1 {
				return true // sweep once
			}
			<-ctx.Done()
			return false
		},
		Logger: discardHealthLogger(),
	})
	checker.WireWriter(writer)
	checker.WireProbe(func(context.Context, int64) ProbeResult { return ProbeResult{Recovered: true, Category: "valid"} })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(writer.recoveries()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := writer.recoveries(); len(got) != 1 || got[0] != 7 {
		t.Fatalf("recoveries = %v, want [7]", got)
	}
	cancel()
	<-done
}

func TestHealthCheckerSweepSyncsRecoveredKeysIntoPool(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{
		{ID: 10, CooldownLevel: 2},
		{ID: 11, CooldownLevel: 1},
	}}
	writer := &stubWriter{failOn: 11, failErr: errors.New("write boom")}
	checker := newTestHealthChecker(t, repo)
	checker.WireWriter(writer)
	checker.WireProbe(func(_ context.Context, id int64) ProbeResult {
		return ProbeResult{Recovered: true, Category: "valid"}
	})
	var mu sync.Mutex
	var synced []int64
	checker.WireSync(func(keyID int64) {
		mu.Lock()
		synced = append(synced, keyID)
		mu.Unlock()
	})

	if err := checker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	mu.Lock()
	got := append([]int64(nil), synced...)
	mu.Unlock()
	// Only key 10 recovered (MarkSuccess succeeded); key 11's write failed and
	// must not be mirrored into pool state.
	want := []int64{10}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("synced = %v, want %v", got, want)
	}
}

func TestHealthCheckerSweepDoesNotSyncUnrecoveredKeys(t *testing.T) {
	repo := &stubHealthRepository{candidates: []keystate.KeySnapshot{{ID: 10, CooldownLevel: 2}}}
	writer := &stubWriter{}
	checker := newTestHealthChecker(t, repo)
	checker.WireWriter(writer)
	checker.WireProbe(func(context.Context, int64) ProbeResult {
		return ProbeResult{Category: "temporarily_unavailable"}
	})
	syncCalls := atomic.Int64{}
	checker.WireSync(func(int64) { syncCalls.Add(1) })

	if err := checker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if syncCalls.Load() != 0 {
		t.Fatalf("sync calls = %d, want 0 for unrecovered keys", syncCalls.Load())
	}
}

func TestHealthCheckerNextDelayShortensToCooldownExpiry(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	checker := NewHealthChecker(&stubHealthRepository{}, &fakeHealthClock{now: now}, HealthCheckerOptions{
		Interval: 10 * time.Minute,
		Logger:   discardHealthLogger(),
	})
	expiry := now.Add(2 * time.Minute)
	checker.WireCooldownExpiry(func(context.Context) (*time.Time, error) { return &expiry, nil })

	if delay := checker.nextDelay(context.Background()); delay != 2*time.Minute+500*time.Millisecond {
		t.Fatalf("nextDelay = %s, want 2m0.5s", delay)
	}
}

func TestHealthCheckerNextDelaySweepsImmediatelyWhenExpired(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	checker := NewHealthChecker(&stubHealthRepository{}, &fakeHealthClock{now: now}, HealthCheckerOptions{
		Interval: 10 * time.Minute,
		Logger:   discardHealthLogger(),
	})
	past := now.Add(-time.Minute)
	checker.WireCooldownExpiry(func(context.Context) (*time.Time, error) { return &past, nil })

	if delay := checker.nextDelay(context.Background()); delay != 0 {
		t.Fatalf("nextDelay = %s, want 0 (already expired)", delay)
	}
}

func TestHealthCheckerNextDelayFallsBackToIntervalWithoutHook(t *testing.T) {
	checker := NewHealthChecker(&stubHealthRepository{}, nil, HealthCheckerOptions{
		Interval: 10 * time.Minute,
		Logger:   discardHealthLogger(),
	})
	if delay := checker.nextDelay(context.Background()); delay != 10*time.Minute {
		t.Fatalf("nextDelay = %s, want 10m without cooldown hook", delay)
	}
}

type fakeHealthClock struct{ now time.Time }

func (c fakeHealthClock) Now() time.Time                     { return c.now }
func (c fakeHealthClock) NewTimer(time.Duration) *time.Timer { return time.NewTimer(time.Hour) }
func (c fakeHealthClock) AfterFunc(time.Duration, func()) *time.Timer {
	return time.NewTimer(time.Hour)
}

func TestRepositoryListKeysForHealthCheckFiltersUnhealthy(t *testing.T) {
	service, db, _ := newNVIDIAKeyTestService(t, newFakeValidator())
	repo := service.repository
	seed := func(suffix string, enabled, authInvalid int, cooldown *string, level, failures int) int64 {
		t.Helper()
		result, err := db.Exec(`INSERT INTO nvidia_keys (ciphertext, nonce, fingerprint, display_prefix, display_suffix, enabled, auth_invalid, cooldown_until, cooldown_level, consecutive_failures, created_at, updated_at) VALUES (x'01', x'02', randomblob(16), ?, ?, ?, ?, ?, ?, ?, '2026-07-30T03:00:00Z', '2026-07-30T03:00:00Z')`,
			"p"+suffix, "s"+suffix, enabled, authInvalid, cooldown, level, failures)
		if err != nil {
			t.Fatalf("seed key %s: %v", suffix, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("seed key id %s: %v", suffix, err)
		}
		return id
	}
	// healthy (no failure history) — excluded
	seed("healthy", 1, 0, nil, 0, 0)
	// disabled — excluded even with failure history
	seed("disabled", 0, 0, nil, 1, 1)
	// auth_invalid — excluded (operator intervention only)
	seed("authinvalid", 1, 1, nil, 0, 1)
	// cooling down, no level/failures — included (cooldown_until set)
	cooldownUntil := "2026-07-30T04:00:00Z"
	cooldownID := seed("cooldown", 1, 0, &cooldownUntil, 0, 0)
	// level>0 — included
	levelID := seed("level", 1, 0, nil, 1, 0)
	// failures>0 — included
	failureID := seed("failures", 1, 0, nil, 0, 1)

	candidates, err := repo.ListKeysForHealthCheck(context.Background())
	if err != nil {
		t.Fatalf("ListKeysForHealthCheck: %v", err)
	}
	got := map[int64]bool{}
	for _, c := range candidates {
		got[c.ID] = true
	}
	want := []int64{cooldownID, levelID, failureID}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("missing candidate id %d in %v", id, got)
		}
	}
	if len(candidates) != len(want) {
		t.Fatalf("candidate count = %d, want %d (got %v)", len(candidates), len(want), got)
	}
}

// Compile-time guard: stubWriter satisfies probeStateWriter.
var _ probeStateWriter = (*stubWriter)(nil)
