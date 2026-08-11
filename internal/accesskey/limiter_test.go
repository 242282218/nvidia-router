package accesskey

import (
	"errors"
	"testing"
	"time"
)

func TestLimiterEnforcesRPMAndConcurrentLimits(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	limiter := newLimiter()
	identity := AccessKeyIdentity{ID: 1, RPMLimit: 1, MaxConcurrent: 1}
	if err := limiter.begin(identity.ID, identity.RPMLimit, 0, identity.MaxConcurrent, 0, 0, now); err != nil {
		t.Fatalf("first begin: %v", err)
	}
	if err := limiter.begin(identity.ID, identity.RPMLimit, 0, identity.MaxConcurrent, 0, 0, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second begin = %v, want rate limited", err)
	}
	limiter.release(identity.ID)
	if err := limiter.begin(identity.ID, identity.RPMLimit, 0, identity.MaxConcurrent, 0, 0, now.Add(time.Minute)); err != nil {
		t.Fatalf("new-window begin: %v", err)
	}
}

func TestLimiterChargesTokensAndResetsWindow(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	limiter := newLimiter()
	if err := limiter.begin(1, 0, 10, 0, 0, 0, now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	limiter.charge(1, 10, 7, 3, now)
	if err := limiter.begin(1, 0, 10, 0, 0, 0, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("token-exhausted begin = %v, want rate limited", err)
	}
	if err := limiter.begin(1, 0, 10, 0, 0, 0, now.Add(time.Minute)); err != nil {
		t.Fatalf("new-window begin: %v", err)
	}
}

func TestLimiterBudgetRejectsAfterConsumedReachesCap(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	limiter := newLimiter()
	const budget int64 = 1000
	// Budget gate is independent of the per-minute TPM window (tpm=0 here), so
	// a budget-only key still accumulates toward its cap.
	if err := limiter.begin(1, 0, 0, 0, budget, 0, now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	limiter.charge(1, 0, 600, 400, now)
	// Exactly at the cap is still allowed (reject is `consumed >= budget` after
	// the charge), so the very next begin observes the exhausted budget.
	if err := limiter.begin(1, 0, 0, 0, budget, 0, now); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("budget-at-cap begin = %v, want ErrBudgetExceeded", err)
	}
	// The budget surviving the minute window rotation is the whole point: a key
	// cannot reset its lifetime cap by waiting.
	if err := limiter.begin(1, 0, 0, 0, budget, 0, now.Add(time.Minute)); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("next-window begin = %v, want ErrBudgetExceeded", err)
	}
}

func TestLimiterBudgetSeedsFromPersistedConsumed(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	limiter := newLimiter()
	const budget int64 = 500
	// Persisted 400 from a previous process: the first request in this process
	// must see the already-spent budget, not start from zero.
	if err := limiter.begin(1, 0, 0, 0, budget, 400, now); err != nil {
		t.Fatalf("begin with persisted budget: %v", err)
	}
	limiter.charge(1, 0, 101, 0, now)
	if err := limiter.begin(1, 0, 0, 0, budget, 400, now); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("seeded over-cap begin = %v, want ErrBudgetExceeded", err)
	}
	if got := limiter.consumedTotal(1); got != 501 {
		t.Fatalf("consumedTotal = %d, want 501 (400 seed + 101 charge)", got)
	}
}

func TestLimiterBudgetUnlimitedWhenZero(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	limiter := newLimiter()
	for range 10 {
		if err := limiter.begin(1, 0, 0, 0, 0, 0, now); err != nil {
			t.Fatalf("unlimited begin: %v", err)
		}
		limiter.charge(1, 0, 100000, 0, now)
	}
	if err := limiter.begin(1, 0, 0, 0, 0, 0, now); err != nil {
		t.Fatalf("unlimited begin after charges: %v", err)
	}
}
