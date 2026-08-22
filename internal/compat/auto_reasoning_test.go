package compat

import "testing"

func TestAutoReasoningSpecChoosesHighestSupportedLevel(t *testing.T) {
	profile := ReasoningProfile{
		Supported:   true,
		Levels:      []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningHigh, ReasoningMax},
		MaxBudget:   128000,
		ZeroAllowed: true,
	}
	spec, ok := AutoReasoningSpec(profile)
	if !ok {
		t.Fatal("AutoReasoningSpec returned no automatic level")
	}
	if spec.Level != ReasoningMax || spec.Source != "auto-inject" {
		t.Fatalf("spec = %#v, want max auto-inject", spec)
	}
}

func TestAutoReasoningSpecSkipsNoneOnlyProfile(t *testing.T) {
	spec, ok := AutoReasoningSpec(ReasoningProfile{
		Supported:   true,
		Levels:      []ReasoningLevel{ReasoningNone},
		ZeroAllowed: true,
	})
	if ok || spec.Requested {
		t.Fatalf("spec = %#v, ok = %v, want no automatic reasoning", spec, ok)
	}
}
