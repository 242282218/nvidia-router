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

// TestBudgetSplitsStreamFirstTokenAndIdleWindows verifies the 014 timeout split:
// a streaming request's prime phase (FirstTokenDeadline) is bounded by
// stream_first_token_timeout_ms, while the transport-level first-byte window and
// the post-commit idle guard keep their own independent values.
func TestBudgetSplitsStreamFirstTokenAndIdleWindows(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := newBudget(runtimeconfig.Snapshot{
		FirstByteTimeoutMS:        2000,
		StreamFirstTokenTimeoutMS: 5000,
		StreamIdleTimeoutMS:       7000,
	}, now, true)
	attempt := budget.forAttempt(now)
	if got := attempt.FirstByteDeadline(); !got.Equal(now.Add(2000 * time.Millisecond)) {
		t.Fatalf("FirstByteDeadline = %v, want %v", got, now.Add(2000*time.Millisecond))
	}
	if got := attempt.FirstTokenDeadline(); !got.Equal(now.Add(5000 * time.Millisecond)) {
		t.Fatalf("FirstTokenDeadline = %v, want %v", got, now.Add(5000*time.Millisecond))
	}
	if got := budget.StreamIdleTimeout(); got != 7000*time.Millisecond {
		t.Fatalf("StreamIdleTimeout = %s, want 7s", got)
	}
}

// TestBudgetStreamTimeoutFallsBackTo1sFloorWhenUnset mirrors the connect/first
// byte fallback: a zero-valued snapshot must still produce usable stream
// windows instead of a zero idle that silently disables the wrap or a deadline
// equal to `now` that fires immediately.
func TestBudgetStreamTimeoutFallsBackTo1sFloorWhenUnset(t *testing.T) {
	floor := 1000 * time.Millisecond
	now := time.Unix(1_700_000_000, 0)
	budget := newBudget(runtimeconfig.Snapshot{}, now, true)
	if got := budget.StreamIdleTimeout(); got != floor {
		t.Fatalf("StreamIdleTimeout = %s, want %s when StreamIdleTimeoutMS unset", got, floor)
	}
	if got := budget.forAttempt(now).FirstTokenDeadline(); !got.Equal(now.Add(floor)) {
		t.Fatalf("FirstTokenDeadline = %v, want %v when StreamFirstTokenTimeoutMS unset", got, now.Add(floor))
	}

	short := 50 * time.Millisecond
	shortBudget := newBudget(runtimeconfig.Snapshot{StreamFirstTokenTimeoutMS: 50, StreamIdleTimeoutMS: 50}, now, true)
	if got := shortBudget.StreamIdleTimeout(); got != short {
		t.Fatalf("StreamIdleTimeout = %s, want %s for explicit small value", got, short)
	}
	if got := shortBudget.forAttempt(now).FirstTokenDeadline(); !got.Equal(now.Add(short)) {
		t.Fatalf("FirstTokenDeadline = %v, want %v for explicit small value", got, now.Add(short))
	}
}

// TestBudgetNonStreamFirstTokenDeadlineFallsBackToFirstByte ensures a non-stream
// budget (no first-token window) reports the first-byte deadline as its prime
// window, keeping the old single-window contract for handlers that share the
// budget accessor.
func TestBudgetNonStreamFirstTokenDeadlineFallsBackToFirstByte(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := newBudget(runtimeconfig.Snapshot{FirstByteTimeoutMS: 2000}, now, false)
	attempt := budget.forAttempt(now)
	if got := attempt.FirstTokenDeadline(); !got.Equal(attempt.FirstByteDeadline()) {
		t.Fatalf("non-stream FirstTokenDeadline = %v, want %v", got, attempt.FirstByteDeadline())
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
