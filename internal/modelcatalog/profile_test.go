package modelcatalog

import (
	"context"
	"reflect"
	"testing"

	"nvidia-router/internal/compat"
)

func TestModelReasoningProfilePersistsAndExposesThinkingWireFormat(t *testing.T) {
	service, _, _, _ := newCatalogTestService(t)
	selection := Selection{
		PublicID: "thinking-profile", UpstreamID: "vendor/thinking-profile", DisplayName: "Thinking profile",
		Kind: KindChat, Enabled: true, SupportsReasoning: true, ReasoningWireFormat: "thinking",
		ReasoningLevels: []string{"low", "medium"}, ReasoningMinBudget: 512, ReasoningMaxBudget: 8192,
		ReasoningZeroAllowed: true, ReasoningDynamicAllowed: false,
	}
	if err := service.SaveSelection(context.Background(), []Selection{selection}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	model, err := service.Resolve(context.Background(), selection.PublicID, Requirements{Kind: KindChat, Reasoning: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(model.ReasoningLevels, selection.ReasoningLevels) || model.ReasoningMinBudget != 512 || model.ReasoningMaxBudget != 8192 || !model.ReasoningZeroAllowed || model.ReasoningDynamicAllowed {
		t.Fatalf("model profile = %+v", model)
	}
	profile := model.ReasoningProfile()
	if profile.WireFormat != "thinking" || !reflect.DeepEqual(profile.Levels, []compat.ReasoningLevel{compat.ReasoningLow, compat.ReasoningMedium}) {
		t.Fatalf("compat profile = %+v", profile)
	}
}

func TestLegacyReasoningModelUsesBroadDefaultProfile(t *testing.T) {
	service, _, _, _ := newCatalogTestService(t)
	selection := Selection{
		PublicID: "legacy-reasoning", UpstreamID: "vendor/legacy-reasoning", DisplayName: "Legacy reasoning",
		Kind: KindChat, Enabled: true, SupportsReasoning: true, ReasoningWireFormat: "openai",
	}
	if err := service.SaveSelection(context.Background(), []Selection{selection}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	model, err := service.Resolve(context.Background(), selection.PublicID, Requirements{Kind: KindChat, Reasoning: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	profile := model.ReasoningProfile()
	if len(profile.Levels) != 8 || profile.MaxBudget != 128000 || !profile.ZeroAllowed || !profile.DynamicAllowed {
		t.Fatalf("legacy profile = %+v", profile)
	}
}
