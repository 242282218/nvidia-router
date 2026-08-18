package admin

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func TestModelDTOExposesReasoningProfile(t *testing.T) {
	dto := toModelDTO(modelcatalog.Model{
		ID: 1, PublicID: "profile", UpstreamID: "vendor/profile", DisplayName: "Profile",
		Kind: modelcatalog.KindChat, ReasoningLevels: []string{"low", "medium"},
		ReasoningMinBudget: 512, ReasoningMaxBudget: 8192, ReasoningZeroAllowed: true,
	})
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal model DTO: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode model DTO: %v", err)
	}
	if fields["reasoning_min_budget"] != float64(512) || fields["reasoning_max_budget"] != float64(8192) || fields["reasoning_zero_allowed"] != true {
		t.Fatalf("reasoning profile fields = %#v", fields)
	}
	levels, ok := fields["reasoning_levels"].([]any)
	if !ok || len(levels) != 2 || levels[0] != "low" || levels[1] != "medium" {
		t.Fatalf("reasoning levels = %#v", fields["reasoning_levels"])
	}
}
