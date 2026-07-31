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

func TestApplySuccessClearsAuthInvalidForAcquireWithSnapshot(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot([]keystate.KeySnapshot{{ID: 1, Enabled: true, AuthInvalid: true}}, nil)

	p.ApplySuccess(1)
	lease, err := p.AcquireWithSnapshot(context.Background(), 100, nil, runtimeconfig.Snapshot{})
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
