package pool

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/runtimeconfig"
)

func TestRoundRobin(t *testing.T) {
	p := New(testSettings{}, fakeClock{now: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)})
	p.LoadSnapshot(testKeys(3, 1, 2), nil)

	for _, wantID := range []int64{1, 2, 3, 1} {
		lease := mustTryAcquire(t, p, 1, nil)
		if got := lease.KeyID(); got != wantID {
			t.Fatalf("Lease.KeyID() = %d, want %d", got, wantID)
		}
		lease.Release()
	}
}

func TestRoundRobinNewPoolStartsWithFirstAvailableKey(t *testing.T) {
	p := New(testSettings{}, fakeClock{now: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)})
	p.LoadSnapshot(testKeys(1, 2, 3), nil)

	lease := mustTryAcquire(t, p, 1, nil)
	lease.Release()

	restarted := New(testSettings{}, fakeClock{now: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)})
	restarted.LoadSnapshot(testKeys(1, 2, 3), nil)
	if got := mustTryAcquire(t, restarted, 1, nil).KeyID(); got != 1 {
		t.Fatalf("first restarted lease key = %d, want 1", got)
	}
}

func TestRoundRobinKeepsNextKeyWhenUpsertSortsOrder(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(2, 3), nil)

	lease := mustTryAcquire(t, p, 1, nil)
	if got := lease.KeyID(); got != 2 {
		t.Fatalf("first lease key = %d, want 2", got)
	}
	lease.Release()
	p.UpsertKey(keystate.KeySnapshot{ID: 1, Enabled: true})

	if got := mustTryAcquire(t, p, 1, nil).KeyID(); got != 3 {
		t.Fatalf("lease after sorted upsert = %d, want 3", got)
	}
}

func TestTryAcquireFiltersUnavailableKeys(t *testing.T) {
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	cooldown := now.Add(time.Minute)
	p := New(testSettings{}, fakeClock{now: now})
	p.LoadSnapshot([]keystate.KeySnapshot{
		{ID: 1, Enabled: false},
		{ID: 2, Enabled: true, AuthInvalid: true},
		{ID: 3, Enabled: true, CooldownUntil: &cooldown},
		{ID: 4, Enabled: true},
		{ID: 5, Enabled: true},
		{ID: 6, Enabled: true},
		{ID: 7, Enabled: true},
	}, []keystate.ModelBlock{{KeyID: 5, ModelID: 99}, {KeyID: 5, ModelID: 100}})

	busy := mustTryAcquire(t, p, 99, map[int64]struct{}{4: {}, 5: {}, 7: {}})
	if got := busy.KeyID(); got != 6 {
		t.Fatalf("busy lease key = %d, want 6", got)
	}

	lease, ok := p.tryAcquire(100, map[int64]struct{}{4: {}})
	if !ok {
		t.Fatal("tryAcquire() did not find the only eligible key")
	}
	if got := lease.KeyID(); got != 7 {
		t.Fatalf("Lease.KeyID() = %d, want 7", got)
	}
	lease.Release()
	busy.Release()
}

func TestUpsertKeyPreservesModelBlocks(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), []keystate.ModelBlock{{KeyID: 1, ModelID: 100}})
	p.UpsertKey(keystate.KeySnapshot{ID: 1, Enabled: true})
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("UpsertKey cleared an existing model block")
	}
}

func TestModelStateChangesSchedulingImmediately(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), nil)
	p.SetModelEnabled(100, false)
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("disabled model was acquired")
	}
	p.SetModelEnabled(100, true)
	lease := mustTryAcquire(t, p, 100, nil)
	lease.Release()
}

func TestStateSyncChangesSchedulingImmediately(t *testing.T) {
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	p := New(testSettings{}, fakeClock{now: now})
	p.LoadSnapshot(testKeys(1, 2), nil)

	p.SetModelBlock(1, 100, true)
	lease := mustTryAcquire(t, p, 100, nil)
	if got := lease.KeyID(); got != 2 {
		t.Fatalf("blocked key selection = %d, want 2", got)
	}
	lease.Release()
	p.SetModelBlock(1, 100, false)
	p.RemoveKey(2)
	lease = mustTryAcquire(t, p, 100, nil)
	if got := lease.KeyID(); got != 1 {
		t.Fatalf("unblocked key selection = %d, want 1", got)
	}
	lease.Release()

	p.UpsertKey(keystate.KeySnapshot{ID: 1, Enabled: false})
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("disabled upserted key was acquired")
	}
	p.UpsertKey(keystate.KeySnapshot{ID: 1, Enabled: true})
	lease = mustTryAcquire(t, p, 100, nil)
	if got := lease.KeyID(); got != 1 {
		t.Fatalf("re-enabled key selection = %d, want 1", got)
	}
	lease.Release()
}

func TestStateSyncAppliesPersistedFailureAndSuccess(t *testing.T) {
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	cooldown := now.Add(time.Minute)
	p := New(testSettings{}, fakeClock{now: now})
	p.LoadSnapshot(testKeys(1), nil)

	p.ApplyFailure(1, 100, fault.Fault{BlockModel: true}, keystate.KeySnapshot{
		ID:                  1,
		Enabled:             true,
		CooldownUntil:       &cooldown,
		CooldownLevel:       2,
		ConsecutiveFailures: 3,
	})
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("failed key was acquired before cooldown elapsed")
	}

	p.ApplySuccess(1)
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("model block was cleared by ApplySuccess")
	}
	p.SetModelBlock(1, 100, false)
	if got := mustTryAcquire(t, p, 100, nil).KeyID(); got != 1 {
		t.Fatalf("success-cleared key selection = %d, want 1", got)
	}
}

func TestApplyFailureDoesNotRollBackConcurrentAdminUpdate(t *testing.T) {
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	cooldown := now.Add(time.Minute)
	p := New(testSettings{}, fakeClock{now: now})
	p.LoadSnapshot(testKeys(1), nil)

	// The admin disable lands in memory after the failure transaction read its
	// snapshot, so the persisted snapshot still carries the stale Enabled=true.
	p.UpsertKey(keystate.KeySnapshot{ID: 1, Enabled: false})
	p.ApplyFailure(1, 100, fault.Fault{}, keystate.KeySnapshot{
		ID:                  1,
		Enabled:             true,
		CooldownUntil:       &cooldown,
		CooldownLevel:       1,
		ConsecutiveFailures: 1,
	})

	state := p.keys[1].snapshot
	if state.Enabled {
		t.Fatal("ApplyFailure rolled back the concurrent admin disable")
	}
	if state.CooldownUntil == nil || !state.CooldownUntil.Equal(cooldown) || state.CooldownLevel != 1 || state.ConsecutiveFailures != 1 {
		t.Fatalf("ApplyFailure did not merge failure fields: %+v", state)
	}
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("disabled key was acquired")
	}
}

func TestApplySuccessClearsAuthInvalidForAcquireWithSnapshot(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot([]keystate.KeySnapshot{{ID: 1, Enabled: true, AuthInvalid: true}}, nil)

	p.ApplySuccess(1)
	lease, err := p.AcquireWithSnapshot(context.Background(), 100, nil, runtimeconfig.Snapshot{}, false)
	if err != nil {
		t.Fatalf("AcquireWithSnapshot after ApplySuccess: %v", err)
	}
	defer lease.Release()
	if got := lease.KeyID(); got != 1 {
		t.Fatalf("Lease.KeyID() = %d, want 1", got)
	}
}

func TestRuntimeSummaryCountsSafePoolState(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	earlyCooldown := now.Add(time.Minute)
	lateCooldown := now.Add(2 * time.Minute)
	expiredCooldown := now.Add(-time.Minute)
	p := New(summarySettings{snapshot: runtimeconfig.Snapshot{QueueCapacity: 7}}, fakeClock{now: now})
	p.LoadSnapshot([]keystate.KeySnapshot{
		{ID: 1, Enabled: true},
		{ID: 2, Enabled: false},
		{ID: 3, Enabled: true, AuthInvalid: true},
		{ID: 4, Enabled: true, CooldownUntil: &lateCooldown},
		{ID: 5, Enabled: true, CooldownUntil: &earlyCooldown},
		{ID: 6, Enabled: true, CooldownUntil: &expiredCooldown},
	}, nil)
	lease := mustTryAcquire(t, p, 1, nil)
	defer lease.Release()

	summary := p.Summary()
	if summary.Keys != (KeyStatusCounts{Total: 6, Enabled: 5, Disabled: 1, AuthInvalid: 1, CoolingDown: 2, Ready: 1}) {
		t.Fatalf("key counts = %#v", summary.Keys)
	}
	if summary.Active != 1 || summary.Queue != (QueueSummary{Capacity: 7}) {
		t.Fatalf("active/queue = %d/%#v", summary.Active, summary.Queue)
	}
	if summary.EarliestCooldown == nil || !summary.EarliestCooldown.Equal(earlyCooldown) {
		t.Fatalf("earliest cooldown = %v, want %v", summary.EarliestCooldown, earlyCooldown)
	}
	if summary.ShuttingDown {
		t.Fatal("new pool reported shutting down")
	}

	p.Shutdown()
	if !p.Summary().ShuttingDown {
		t.Fatal("shutdown pool did not report shutting_down")
	}
}

func TestLeaseReleaseIsIdempotent(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), nil)

	lease := mustTryAcquire(t, p, 1, nil)
	lease.Release()
	lease.Release()
	if got := mustTryAcquire(t, p, 1, nil).KeyID(); got != 1 {
		t.Fatalf("released key selection = %d, want 1", got)
	}
}

func TestConcurrencySingleLeasePerKey(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), nil)

	start := make(chan struct{})
	var workers sync.WaitGroup
	var state sync.Mutex
	active := make(map[int64]int)
	maximum := make(map[int64]int)

	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for {
				lease, ok := p.tryAcquire(1, nil)
				if !ok {
					runtime.Gosched()
					continue
				}
				state.Lock()
				active[lease.KeyID()]++
				if active[lease.KeyID()] > maximum[lease.KeyID()] {
					maximum[lease.KeyID()] = active[lease.KeyID()]
				}
				state.Unlock()

				runtime.Gosched()

				state.Lock()
				active[lease.KeyID()]--
				state.Unlock()
				lease.Release()
				return
			}
		}()
	}
	close(start)
	workers.Wait()

	for keyID := int64(1); keyID <= 10; keyID++ {
		if got := maximum[keyID]; got != 1 {
			t.Fatalf("key %d maximum concurrent leases = %d, want 1", keyID, got)
		}
	}
}

func mustTryAcquire(t *testing.T, p *Pool, modelID int64, attempted map[int64]struct{}) Lease {
	t.Helper()
	lease, ok := p.tryAcquire(modelID, attempted)
	if !ok {
		t.Fatal("tryAcquire() did not return a lease")
	}
	return lease
}

func testKeys(ids ...int64) []keystate.KeySnapshot {
	keys := make([]keystate.KeySnapshot, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, keystate.KeySnapshot{ID: id, Enabled: true})
	}
	return keys
}

type testSettings struct{}

func (testSettings) Snapshot() runtimeconfig.Snapshot { return runtimeconfig.Snapshot{} }

type fakeClock struct {
	now time.Time
}

func (c fakeClock) Now() time.Time                   { return c.now }
func (fakeClock) NewTimer(time.Duration) *time.Timer { return time.NewTimer(time.Hour) }
func (fakeClock) AfterFunc(time.Duration, func()) *time.Timer {
	return time.NewTimer(time.Hour)
}

type summarySettings struct {
	snapshot runtimeconfig.Snapshot
}

func (s summarySettings) Snapshot() runtimeconfig.Snapshot { return s.snapshot }

// TestLatencySchedulingPrefersFasterKey uses an injected RNG so the weighted
// draw is deterministic: rng returning ~0 lands at the start of the weighted
// interval, which is the fastest key.
func TestLatencySchedulingPrefersFasterKey(t *testing.T) {
	p := New(summarySettings{snapshot: runtimeconfig.Snapshot{LatencyRoutingEnabled: true}}, fakeClock{now: time.Unix(0, 0).UTC()})
	// Deterministic rng cycling 0.000..0.999 so every weighted interval is hit
	// in proportion: faster keys should dominate without starving unwarmed keys.
	drawIndex := 0
	p.latencyRNG = func() float64 {
		value := float64(drawIndex%1000) / 1000
		drawIndex++
		return value
	}
	p.LoadSnapshot(testKeys(1, 2, 3), nil)
	// Give key 2 a much larger (slower) EWMA than key 1; key 3 stays unwarmed.
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)
	p.RecordLatency(2, 3000)
	p.RecordLatency(2, 3000)
	p.RecordLatency(2, 3000)

	counts := map[int64]int{}
	for range 1000 {
		lease, err := p.AcquireWithSnapshot(context.Background(), 100, map[int64]struct{}{}, runtimeconfig.Snapshot{LatencyRoutingEnabled: true}, false)
		if err != nil {
			t.Fatalf("latency acquire: %v", err)
		}
		counts[lease.KeyID()]++
		lease.Release()
	}
	if counts[1] <= counts[2] {
		t.Fatalf("fastest key 1 not dominant: %v", counts)
	}
	if counts[3] == 0 {
		t.Fatalf("unwarmed key 3 starved entirely despite exploration weight: %v", counts)
	}
}

// TestLatencySchedulingOffKeepsRoundRobin confirms the feature gate restores
// the legacy behaviour exactly.
func TestLatencySchedulingOffKeepsRoundRobin(t *testing.T) {
	p := New(summarySettings{snapshot: runtimeconfig.Snapshot{LatencyRoutingEnabled: false}}, fakeClock{now: time.Unix(0, 0).UTC()})
	p.LoadSnapshot(testKeys(1, 2, 3), nil)
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)
	p.RecordLatency(2, 3000)
	p.RecordLatency(2, 3000)
	p.RecordLatency(2, 3000)

	order := []int64{}
	for range 3 {
		lease, ok := p.tryAcquire(100, map[int64]struct{}{})
		if !ok {
			t.Fatal("round-robin acquire failed")
		}
		order = append(order, lease.KeyID())
		lease.Release()
	}
	if order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("round-robin order = %v, want [1 2 3]", order)
	}
}

func TestLatencySchedulingWarmupFallsBackToUniform(t *testing.T) {
	p := New(summarySettings{snapshot: runtimeconfig.Snapshot{LatencyRoutingEnabled: true}}, fakeClock{now: time.Unix(0, 0).UTC()})
	p.latencyRNG = func() float64 { return 0.9 }
	p.LoadSnapshot(testKeys(1, 2), nil)
	// Only key 1 is measured; weightedSelect with measured < 2 must fall back to
	// a uniform pick (rng 0.9 → last index = key 2).
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)

	lease, err := p.AcquireWithSnapshot(context.Background(), 100, map[int64]struct{}{}, runtimeconfig.Snapshot{LatencyRoutingEnabled: true}, false)
	if err != nil {
		t.Fatalf("warmup acquire: %v", err)
	}
	if lease.KeyID() != 2 {
		t.Fatalf("warmup fallback selected key %d, want 2 (uniform draw with rng 0.9)", lease.KeyID())
	}
	lease.Release()
}

func TestRecordLatencyIgnoresNonPositiveDurations(t *testing.T) {
	p := New(summarySettings{snapshot: runtimeconfig.Snapshot{LatencyRoutingEnabled: true}}, fakeClock{now: time.Unix(0, 0).UTC()})
	p.LoadSnapshot(testKeys(1, 2), nil)
	// Zero/negative durations must not pollute the EWMA: key 1 stays unwarmed.
	p.RecordLatency(1, 0)
	p.RecordLatency(1, -5)
	p.RecordLatency(1, 100)
	p.RecordLatency(1, 100)
	if state := p.keys[1]; state.latencySamples != 2 {
		t.Fatalf("key 1 latencySamples = %d, want 2 (non-positive durations ignored)", state.latencySamples)
	}
	state := p.keys[1]
	if got, measured := state.latencyScore(); measured && got <= 0 {
		t.Fatalf("key 1 latencyScore = %f, want positive EWMA", got)
	}
}
