package compat

import (
	"encoding/json"
	"fmt"
	"testing"
)

// thinkingProfile is the wire format that actually carries a numeric budget to
// the upstream, so it is the only one where capping is observable.
func thinkingProfile() ReasoningProfile {
	return ReasoningProfile{
		Supported:   true,
		Levels:      []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningHigh},
		MaxBudget:   128000,
		ZeroAllowed: true,
		WireFormat:  "thinking",
	}
}

func applyForTest(t *testing.T, fields map[string]json.RawMessage, profile ReasoningProfile) {
	t.Helper()
	spec, err := ParseReasoning(fields)
	if err != nil {
		t.Fatalf("ParseReasoning: %v", err)
	}
	decision, err := ResolveReasoning(spec, profile)
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if err := ApplyReasoning(fields, decision, profile); err != nil {
		t.Fatalf("ApplyReasoning: %v", err)
	}
}

func TestThinkingBudgetCappedToCompletionAllowance(t *testing.T) {
	// high resolves to 24576, far beyond the 1024 the client allowed for the
	// whole completion; without a cap the upstream spends every token thinking
	// and returns empty content with finish_reason=length.
	fields := map[string]json.RawMessage{
		"max_tokens":       json.RawMessage(`1024`),
		"reasoning_effort": json.RawMessage(`"high"`),
	}
	applyForTest(t, fields, thinkingProfile())
	if got := string(fields["thinking"]); got != `{"budget_tokens":768,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want budget capped to 768", got)
	}
}

func TestThinkingBudgetLeftAloneWhenItFits(t *testing.T) {
	fields := map[string]json.RawMessage{
		"max_tokens":       json.RawMessage(`8192`),
		"reasoning_effort": json.RawMessage(`"low"`),
	}
	applyForTest(t, fields, thinkingProfile())
	if got := string(fields["thinking"]); got != `{"budget_tokens":1024,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want the unmodified low budget", got)
	}
}

func TestThinkingBudgetUncappedWithoutClientLimit(t *testing.T) {
	// No completion limit means the upstream default applies; there is nothing
	// to reconcile against, so the level's budget must survive untouched.
	fields := map[string]json.RawMessage{"reasoning_effort": json.RawMessage(`"high"`)}
	applyForTest(t, fields, thinkingProfile())
	if got := string(fields["thinking"]); got != `{"budget_tokens":24576,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want the uncapped high budget", got)
	}
}

func TestThinkingDisabledIgnoresCap(t *testing.T) {
	fields := map[string]json.RawMessage{
		"max_tokens": json.RawMessage(`64`),
		"thinking":   json.RawMessage(`{"type":"disabled"}`),
	}
	applyForTest(t, fields, thinkingProfile())
	if got := string(fields["thinking"]); got != `{"type":"disabled"}` {
		t.Fatalf("thinking = %s, want disabled", got)
	}
}

func TestExplicitNativeBudgetAlsoCapped(t *testing.T) {
	// A client-supplied budget is just as capable of starving the answer as a
	// level-derived one, so the same reconciliation applies.
	profile := thinkingProfile()
	profile.DynamicAllowed = true
	fields := map[string]json.RawMessage{
		"max_tokens": json.RawMessage(`2000`),
		"thinking":   json.RawMessage(`{"type":"enabled","budget_tokens":100000}`),
	}
	applyForTest(t, fields, profile)
	if got := string(fields["thinking"]); got != `{"budget_tokens":1500,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want budget capped to 1500", got)
	}
}

func TestMaxCompletionTokensCountsAsAllowance(t *testing.T) {
	// Chat applies reasoning before max_completion_tokens is renamed, so the cap
	// has to recognise the pre-normalisation spelling too.
	fields := map[string]json.RawMessage{
		"max_completion_tokens": json.RawMessage(`400`),
		"reasoning_effort":      json.RawMessage(`"high"`),
	}
	applyForTest(t, fields, thinkingProfile())
	if got := string(fields["thinking"]); got != `{"budget_tokens":300,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want budget capped to 300", got)
	}
}

func TestThinkingBudgetAbsoluteReserveAtTinyLimit(t *testing.T) {
	// 75% of 64 is 48, but a 16-token remainder starves even a one-line reply;
	// the absolute reserve binds instead and leaves exactly reserve tokens.
	fields := map[string]json.RawMessage{
		"max_tokens":       json.RawMessage(`64`),
		"reasoning_effort": json.RawMessage(`"high"`),
	}
	applyForTest(t, fields, thinkingProfile())
	if got := string(fields["thinking"]); got != `{"budget_tokens":32,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want budget capped to the 32-token reserve", got)
	}
}

func TestThinkingBudgetReserveBindsBelowCrossover(t *testing.T) {
	// Below limit 128 the absolute reserve beats the 75% share; at 128 they
	// agree (96) and above it the percentage wins again.
	for _, testCase := range []struct{ limit, want int }{
		{120, 88},
		{128, 96},
		{200, 150},
	} {
		fields := map[string]json.RawMessage{
			"max_tokens":       json.RawMessage(fmt.Sprintf(`%d`, testCase.limit)),
			"reasoning_effort": json.RawMessage(`"high"`),
		}
		applyForTest(t, fields, thinkingProfile())
		want := fmt.Sprintf(`{"budget_tokens":%d,"type":"enabled"}`, testCase.want)
		if got := string(fields["thinking"]); got != want {
			t.Fatalf("limit %d: thinking = %s, want %s", testCase.limit, got, want)
		}
	}
}

func TestOpenAIWireIgnoresBudgetCap(t *testing.T) {
	// The openai wire carries no numeric budget at all, so capping must not
	// change what reaches the upstream.
	profile := thinkingProfile()
	profile.WireFormat = "openai"
	fields := map[string]json.RawMessage{
		"max_tokens":       json.RawMessage(`64`),
		"reasoning_effort": json.RawMessage(`"high"`),
	}
	applyForTest(t, fields, profile)
	if got := string(fields["reasoning_effort"]); got != `"high"` {
		t.Fatalf("reasoning_effort = %s, want high", got)
	}
	if _, ok := fields["thinking"]; ok {
		t.Fatalf("openai wire must not emit a thinking object")
	}
}

func TestAdvisoryLevelsCollapseToOnOff(t *testing.T) {
	// Every enabled level means the same thing to an advisory upstream, so they
	// all leave as one standard value; only "none" stays distinguishable.
	profile := thinkingProfile()
	profile.WireFormat = "openai"
	profile.AdvisoryLevels = true
	profile.Levels = []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh}
	for _, requested := range []string{"low", "medium", "high"} {
		fields := map[string]json.RawMessage{
			"reasoning_effort": json.RawMessage(`"` + requested + `"`),
		}
		applyForTest(t, fields, profile)
		if got := string(fields["reasoning_effort"]); got != `"high"` {
			t.Fatalf("%s -> %s, want collapsed to high", requested, got)
		}
	}
	off := map[string]json.RawMessage{"reasoning_effort": json.RawMessage(`"none"`)}
	applyForTest(t, off, profile)
	if got := string(off["reasoning_effort"]); got != `"none"` {
		t.Fatalf("none -> %s, want none preserved", got)
	}
}

func TestNonAdvisoryLevelsKeepGradient(t *testing.T) {
	profile := thinkingProfile()
	profile.WireFormat = "openai"
	profile.Levels = []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningHigh}
	fields := map[string]json.RawMessage{"reasoning_effort": json.RawMessage(`"low"`)}
	applyForTest(t, fields, profile)
	if got := string(fields["reasoning_effort"]); got != `"low"` {
		t.Fatalf("reasoning_effort = %s, want low preserved", got)
	}
}
