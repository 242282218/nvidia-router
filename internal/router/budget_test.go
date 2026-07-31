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
