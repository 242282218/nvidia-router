package pool

import (
	"testing"
	"time"

	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
)

// TestSetKeyEnabledPreservesCooldown is the mirror of
// TestApplyFailureDoesNotRollBackConcurrentAdminUpdate. That test covers a
// failure landing after an admin update; this one covers the opposite ordering.
//
// The admin SetEnabled handler used UpsertKey, which replaces the whole
// snapshot. When ApplyFailure had already written a cooldown into memory, the
// admin's snapshot (read in an earlier transaction) reverted it, leaving the DB
// in cooldown while the pool believed the key was ready — so the scheduler
// handed out a key it should have skipped.
func TestSetKeyEnabledPreservesCooldown(t *testing.T) {
	now := time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)
	cooldown := now.Add(time.Minute)
	p := New(testSettings{}, fakeClock{now: now})
	p.LoadSnapshot(testKeys(1), nil)

	p.ApplyFailure(1, 100, fault.Fault{}, keystate.KeySnapshot{
		ID:                  1,
		Enabled:             true,
		CooldownUntil:       &cooldown,
		CooldownLevel:       2,
		ConsecutiveFailures: 3,
	})

	// Admin re-enables using a snapshot read before the failure committed.
	p.SetKeyEnabled(1, true)

	state := p.keys[1].snapshot
	if state.CooldownUntil == nil || !state.CooldownUntil.Equal(cooldown) {
		t.Fatalf("SetKeyEnabled cleared CooldownUntil: %+v", state)
	}
	if state.CooldownLevel != 2 || state.ConsecutiveFailures != 3 {
		t.Fatalf("SetKeyEnabled reset failure counters: %+v", state)
	}
	if !state.Enabled {
		t.Fatal("SetKeyEnabled did not apply Enabled")
	}
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("key in cooldown was acquired after SetKeyEnabled")
	}
}

// TestSetKeyEnabledDisableTakesEffect verifies the admin-owned field is still
// applied — preserving failure state must not make the method a no-op.
func TestSetKeyEnabledDisableTakesEffect(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), nil)

	p.SetKeyEnabled(1, false)

	if p.keys[1].snapshot.Enabled {
		t.Fatal("SetKeyEnabled(false) did not disable the key")
	}
	if _, ok := p.tryAcquire(100, nil); ok {
		t.Fatal("disabled key was acquired")
	}
}

// TestUpsertKeyStillReplacesSnapshot documents that UpsertKey keeps its
// replacing semantics. The Test/ProbeHealth path depends on it: a successful
// credential test legitimately clears auth_invalid and the cooldown.
func TestUpsertKeyStillReplacesSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)
	cooldown := now.Add(time.Minute)
	p := New(testSettings{}, fakeClock{now: now})
	p.LoadSnapshot([]keystate.KeySnapshot{{
		ID: 1, Enabled: true, AuthInvalid: true,
		CooldownUntil: &cooldown, CooldownLevel: 2, ConsecutiveFailures: 3,
	}}, nil)

	p.UpsertKey(keystate.KeySnapshot{ID: 1, Enabled: true})

	state := p.keys[1].snapshot
	if state.CooldownUntil != nil || state.AuthInvalid || state.CooldownLevel != 0 || state.ConsecutiveFailures != 0 {
		t.Fatalf("UpsertKey no longer replaces the snapshot: %+v", state)
	}
	if _, ok := p.tryAcquire(100, nil); !ok {
		t.Fatal("key recovered by a successful test was not acquirable")
	}
}

func TestSetKeyEnabledIgnoresUnknownKey(t *testing.T) {
	p := New(testSettings{}, fakeClock{})
	p.LoadSnapshot(testKeys(1), nil)

	p.SetKeyEnabled(999, false)

	if len(p.keys) != 1 || !p.keys[1].snapshot.Enabled {
		t.Fatal("SetKeyEnabled on an unknown key mutated pool state")
	}
}
