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
	spec, ok := AutoReasoningSpec(profile, 0)
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
	spec, ok := AutoReasoningSpec(profile, 0)
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
	spec, ok := AutoReasoningSpec(profile, 0)
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
	}, 0)
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
	}, 0)
	if ok || spec.Requested {
		t.Fatalf("spec = %#v, ok = %v, want no automatic reasoning", spec, ok)
	}
}

// A tiny completion window cannot survive any reasoning: the ladder injects
// "off" so the upstream does not spend every token thinking and return an
// empty answer with finish_reason=length (observed on hy3/kimi-k3 at 64).
func TestAutoReasoningSpecSuppressesInjectionUnderTinyLimit(t *testing.T) {
	profile := ReasoningProfile{
		Supported:      true,
		MaxBudget:      128000,
		ZeroAllowed:    true,
		DynamicAllowed: true,
	}
	spec, ok := AutoReasoningSpec(profile, 100)
	if !ok {
		t.Fatal("AutoReasoningSpec returned no automatic level")
	}
	if spec.Level != ReasoningNone || spec.Budget != 0 || spec.Source != "auto-inject" {
		t.Fatalf("spec = %#v, want none auto-inject", spec)
	}
}

// A profile that cannot express "off" (none listed but ZeroAllowed=false,
// e.g. the llama shape) stays silent instead of injecting a positive level:
// silence never produces ErrReasoningUnsupported, while a positive level
// guarantees starvation.
func TestAutoReasoningSpecSkipsWhenOffUnavailableUnderTinyLimit(t *testing.T) {
	spec, ok := AutoReasoningSpec(ReasoningProfile{
		Supported:   true,
		Levels:      []ReasoningLevel{ReasoningNone},
		ZeroAllowed: false,
	}, 100)
	if ok || spec.Requested {
		t.Fatalf("spec = %#v, ok = %v, want no injection", spec, ok)
	}
}

// The small band gets only the cheapest positive level; anything heavier
// would eat the window before the answer started.
func TestAutoReasoningSpecInjectsCheapestPositiveForSmallLimits(t *testing.T) {
	profile := ReasoningProfile{
		Supported:      true,
		Levels:         []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningHigh, ReasoningMax},
		MaxBudget:      128000,
		ZeroAllowed:    true,
		DynamicAllowed: true,
	}
	for _, limit := range []int{129, 300, autoReasoningSmallCeiling} {
		spec, ok := AutoReasoningSpec(profile, limit)
		if !ok || spec.Level != ReasoningLow {
			t.Fatalf("limit %d: spec = %#v ok = %v, want low", limit, spec, ok)
		}
	}
	allHeavy := profile
	allHeavy.Levels = []ReasoningLevel{ReasoningNone, ReasoningHigh, ReasoningMax}
	spec, ok := AutoReasoningSpec(allHeavy, 200)
	if !ok || spec.Level != ReasoningHigh {
		t.Fatalf("all-heavy spec = %#v ok = %v, want high", spec, ok)
	}
}

// At the small-band boundary and above, the moderate default applies again.
func TestAutoReasoningSpecModerateFrom512Up(t *testing.T) {
	profile := ReasoningProfile{
		Supported:      true,
		Levels:         []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh},
		MaxBudget:      128000,
		ZeroAllowed:    true,
		DynamicAllowed: true,
	}
	for _, limit := range []int{autoReasoningSmallCeiling + 1, 512, 4000, 0} {
		spec, ok := AutoReasoningSpec(profile, limit)
		if !ok || spec.Level != ReasoningMedium {
			t.Fatalf("limit %d: spec = %#v ok = %v, want medium", limit, spec, ok)
		}
	}
}
