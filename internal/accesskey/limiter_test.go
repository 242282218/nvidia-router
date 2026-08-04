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
	if err := limiter.begin(identity.ID, identity.RPMLimit, 0, identity.MaxConcurrent, now); err != nil {
		t.Fatalf("first begin: %v", err)
	}
	if err := limiter.begin(identity.ID, identity.RPMLimit, 0, identity.MaxConcurrent, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second begin = %v, want rate limited", err)
	}
	limiter.release(identity.ID)
	if err := limiter.begin(identity.ID, identity.RPMLimit, 0, identity.MaxConcurrent, now.Add(time.Minute)); err != nil {
		t.Fatalf("new-window begin: %v", err)
	}
}

func TestLimiterChargesTokensAndResetsWindow(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	limiter := newLimiter()
	if err := limiter.begin(1, 0, 10, 0, now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	limiter.charge(1, 10, 7, 3, now)
	if err := limiter.begin(1, 0, 10, 0, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("token-exhausted begin = %v, want rate limited", err)
	}
	if err := limiter.begin(1, 0, 10, 0, now.Add(time.Minute)); err != nil {
		t.Fatalf("new-window begin: %v", err)
	}
}
