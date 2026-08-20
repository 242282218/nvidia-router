package modelhealth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateSettingsAcceptsSafeDefaults(t *testing.T) {
	settings := DefaultSettings()
	if err := ValidateSettings(settings); err != nil {
		t.Fatalf("ValidateSettings(defaults): %v", err)
	}
	if settings.IntervalSeconds != 60 || settings.Concurrency != 2 || settings.Enabled {
		t.Fatalf("defaults = %+v, want disabled/60s/concurrency2", settings)
	}
}

func TestValidateSettingsRejectsUnsafeFrequencyAndConcurrency(t *testing.T) {
	tests := []Settings{
		{IntervalSeconds: 9, Concurrency: 2},
		{IntervalSeconds: 3601, Concurrency: 2},
		{IntervalSeconds: 60, Concurrency: 0},
		{IntervalSeconds: 60, Concurrency: 9},
	}
	for _, settings := range tests {
		if err := ValidateSettings(settings); err == nil {
			t.Fatalf("ValidateSettings(%+v) succeeded", settings)
		}
	}
}

func TestClassifyStatusSeparatesMixedWindowFromUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	interval := time.Minute
	tests := []struct {
		name   string
		latest *Latest
		stats  WindowStats
		want   Status
	}{
		{name: "never probed", want: StatusUnchecked},
		{
			name:   "recent success",
			latest: &Latest{Outcome: OutcomeSuccess, LastProbeAt: now.Add(-10 * time.Second)},
			stats:  WindowStats{ProbeCount: 1, SuccessCount: 1},
			want:   StatusHealthy,
		},
		{
			name:   "mixed results",
			latest: &Latest{Outcome: OutcomeSuccess, LastProbeAt: now.Add(-10 * time.Second)},
			stats:  WindowStats{ProbeCount: 4, SuccessCount: 3, FailureCount: 1},
			want:   StatusDegraded,
		},
		{
			name:   "consecutive failures",
			latest: &Latest{Outcome: OutcomeFailure, ConsecutiveFailures: 3, LastProbeAt: now.Add(-10 * time.Second)},
			stats:  WindowStats{ProbeCount: 3, FailureCount: 3},
			want:   StatusUnavailable,
		},
		{
			name:   "stale",
			latest: &Latest{Outcome: OutcomeSuccess, LastProbeAt: now.Add(-3 * time.Minute)},
			stats:  WindowStats{ProbeCount: 1, SuccessCount: 1},
			want:   StatusStale,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyStatus(test.latest, test.stats, now, interval); got != test.want {
				t.Fatalf("ClassifyStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildBucketsCreatesFixedTimelineAndMixedOutcome(t *testing.T) {
	from := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	events := []ProbeEvent{
		{ModelID: 7, Outcome: OutcomeSuccess, DurationMS: 100, CreatedAt: from.Add(10 * time.Minute)},
		{ModelID: 7, Outcome: OutcomeFailure, DurationMS: 300, CreatedAt: from.Add(11 * time.Minute)},
		{ModelID: 7, Outcome: OutcomeTimeout, DurationMS: 1000, CreatedAt: from.Add(70 * time.Minute)},
	}
	buckets := BuildBuckets(from, to, 4, events)
	if len(buckets) != 4 {
		t.Fatalf("bucket count = %d, want 4", len(buckets))
	}
	if buckets[0].Outcome != OutcomeMixed || buckets[0].ProbeCount != 2 || buckets[0].SuccessCount != 1 {
		t.Fatalf("first bucket = %+v, want mixed 2/1", buckets[0])
	}
	if buckets[2].Outcome != OutcomeTimeout || buckets[2].ProbeCount != 1 {
		t.Fatalf("third bucket = %+v, want timeout/1", buckets[2])
	}
	if buckets[1].Outcome != OutcomeEmpty || buckets[3].Outcome != OutcomeEmpty {
		t.Fatalf("empty buckets = %+v/%+v", buckets[1], buckets[3])
	}
}

func TestClassifyProbeErrorUsesSafeCategories(t *testing.T) {
	if got := ClassifyProbeError(contextCanceledError{}); got != OutcomeCanceled {
		t.Fatalf("canceled category = %q, want %q", got, OutcomeCanceled)
	}
	if got := ClassifyProbeError(timeoutError{}); got != OutcomeTimeout {
		t.Fatalf("timeout category = %q, want %q", got, OutcomeTimeout)
	}
	if got := ClassifyProbeError(errors.New("upstream detail must not be persisted")); got != OutcomeFailure {
		t.Fatalf("failure category = %q, want %q", got, OutcomeFailure)
	}
}

// These tiny test errors keep the category contract independent of a concrete
// HTTP implementation.
type contextCanceledError struct{}

func (contextCanceledError) Error() string { return "context canceled" }
func (contextCanceledError) Unwrap() error { return context.Canceled }

type timeoutError struct{}

func (timeoutError) Error() string { return "deadline exceeded" }
func (timeoutError) Timeout() bool { return true }
func (timeoutError) Unwrap() error { return context.DeadlineExceeded }
