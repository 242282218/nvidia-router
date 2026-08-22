package compat

import "testing"

// The silent auto-inject default must be moderate. Requests that never
// mentioned reasoning get this level on every call, so the heaviest level
// would silently give them the slowest path — and for upstreams whose
// vocabulary stops below the top (e.g. no_think/low/high) the injected max
// was rejected outright (hy3-free answered 400 on every plain request).
func TestAutoReasoningSpecPicksModerateDefault(t *testing.T) {
	profile := ReasoningProfile{
		Supported:      true,
		MaxBudget:      128000,
		ZeroAllowed:    true,
		DynamicAllowed: true,
	}
	spec, ok := AutoReasoningSpec(profile)
	if !ok {
		t.Fatal("AutoReasoningSpec returned no automatic level")
	}
	if spec.Level != ReasoningMedium || spec.Source != "auto-inject" {
		t.Fatalf("spec = %#v, want medium auto-inject", spec)
	}
}

// Restricted vocabulary: the strongest level no heavier than medium wins, so
// a low/high profile auto-injects low instead of the rejected high/max.
func TestAutoReasoningSpecChoosesHeaviestLevelWithinMediumBudget(t *testing.T) {
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
	if spec.Level != ReasoningLow {
		t.Fatalf("spec = %#v, want low", spec)
	}
}

// When every level is heavier than medium the cheapest one is injected —
// still never the heaviest available.
func TestAutoReasoningSpecFallsBackToCheapestWhenAllLevelsHeavier(t *testing.T) {
	profile := ReasoningProfile{
		Supported:   true,
		Levels:      []ReasoningLevel{ReasoningHigh, ReasoningXHigh, ReasoningMax},
		MaxBudget:   128000,
		ZeroAllowed: true,
	}
	spec, ok := AutoReasoningSpec(profile)
	if !ok {
		t.Fatal("AutoReasoningSpec returned no automatic level")
	}
	if spec.Level != ReasoningHigh {
		t.Fatalf("spec = %#v, want high", spec)
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

// "auto" is a dynamic sentinel, not a wire-safe OpenAI effort value; the
// injector must skip profiles that only offer none+auto rather than emit it.
func TestAutoReasoningSpecSkipsAutoSentinel(t *testing.T) {
	spec, ok := AutoReasoningSpec(ReasoningProfile{
		Supported:   true,
		Levels:      []ReasoningLevel{ReasoningNone, ReasoningAuto},
		ZeroAllowed: true,
	})
	if ok || spec.Requested {
		t.Fatalf("spec = %#v, ok = %v, want no automatic reasoning", spec, ok)
	}
}
