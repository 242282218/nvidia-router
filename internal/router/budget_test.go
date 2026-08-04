package router

import (
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func TestBudgetExposesStableFirstByteTimeout(t *testing.T) {
	want := 3500 * time.Millisecond
	budget := newBudget(runtimeconfig.Snapshot{FirstByteTimeoutMS: 3500}, time.Now(), true)
	if got := budget.FirstByteTimeout(); got != want {
		t.Fatalf("FirstByteTimeout = %s, want %s", got, want)
	}
}

// TestBudgetFallsBackTo1sFloorWhenUnset mirrors pool.resolveQueueSettings: a
// zero-valued snapshot (uninitialised provider, fresh test) must still produce
// a usable budget instead of a 0 connect timeout and a deadline equal to now
// that fires immediately. Small positive values are honoured, only <= 0 is
// clamped, matching the symmetry with the queue settings resolver.
func TestBudgetFallsBackTo1sFloorWhenUnset(t *testing.T) {
	floor := 1000 * time.Millisecond
	now := time.Unix(1_700_000_000, 0)
	budget := newBudget(runtimeconfig.Snapshot{}, now, false)
	if got := budget.ConnectTimeout(); got != floor {
		t.Fatalf("ConnectTimeout = %s, want %s when ConnectTimeoutMS unset", got, floor)
	}
	if got := budget.FirstByteTimeout(); got != floor {
		t.Fatalf("FirstByteTimeout = %s, want %s when FirstByteTimeoutMS unset", got, floor)
	}
	if budget.totalDeadline != now.Add(floor) {
		t.Fatalf("totalDeadline = %s, want %s when NonstreamTotalTimeoutMS unset", budget.totalDeadline, now.Add(floor))
	}

	// Small positive values must pass through untouched: tests exercise
	// sub-second budgets, and clamping them to 1s would change the timing
	// contract they assert on.
	short := 50 * time.Millisecond
	shortBudget := newBudget(runtimeconfig.Snapshot{ConnectTimeoutMS: 50, FirstByteTimeoutMS: 50, NonstreamTotalTimeoutMS: 50}, now, false)
	if got := shortBudget.ConnectTimeout(); got != short {
		t.Fatalf("ConnectTimeout = %s, want %s for explicit small value", got, short)
	}
	if got := shortBudget.FirstByteTimeout(); got != short {
		t.Fatalf("FirstByteTimeout = %s, want %s for explicit small value", got, short)
	}
	if shortBudget.totalDeadline != now.Add(short) {
		t.Fatalf("totalDeadline = %s, want %s for explicit small value", shortBudget.totalDeadline, now.Add(short))
	}
}
