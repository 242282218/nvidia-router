package observability

import (
	"context"
	"testing"
)

func TestRequestStateStoresReasoningRequestedAndEffectiveLevels(t *testing.T) {
	ctx, state := WithRequestState(context.Background())
	SetReasoningRequest(ctx, true, "thinking")
	SetReasoningLevels(ctx, "high", "medium")
	snapshot := state.Snapshot()
	if snapshot.ReasoningRequestedLevel != "high" || snapshot.ReasoningEffectiveLevel != "medium" {
		t.Fatalf("reasoning levels = %q/%q", snapshot.ReasoningRequestedLevel, snapshot.ReasoningEffectiveLevel)
	}
}
