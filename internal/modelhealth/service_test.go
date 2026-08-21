package modelhealth

import (
	"testing"
	"time"
)

func TestClassifyStatusDistinguishesKeyCoolingFromUnconfigured(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	interval := time.Minute

	// Case 1: keys exist (count=2) but all are cooling (FirstEnabledID returns sql.ErrNoRows)
	latest := &Latest{
		Outcome:     OutcomeSkipped,
		ErrorCode:   "keys_cooling",
		LastProbeAt: now,
	}
	stats := WindowStats{ProbeCount: 1}
	status := ClassifyStatus(latest, stats, now, interval)
	if status == StatusUnconfigured {
		t.Errorf("ClassifyStatus with keys_cooling = %v; want != StatusUnconfigured", status)
	}

	// Case 2: truly no keys configured
	latest = &Latest{
		Outcome:     OutcomeSkipped,
		ErrorCode:   "no_credential",
		LastProbeAt: now,
	}
	status = ClassifyStatus(latest, stats, now, interval)
	if status != StatusUnconfigured {
		t.Errorf("ClassifyStatus with no_credential = %v; want StatusUnconfigured", status)
	}
}
