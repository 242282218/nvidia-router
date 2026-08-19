package compat

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeToolDefinitionsAcceptsChatAndResponsesShapes(t *testing.T) {
	nested := []byte(`[{"type":"function","function":{"name":"lookup","description":"Look up","parameters":{"type":"object"},"strict":true}}]`)
	flat := []byte(`[{"type":"function","name":"lookup","description":"Look up","parameters":{"type":"object"},"strict":true}]`)

	want, err := NormalizeTools(nested, ToolFormatChat, "tools")
	if err != nil {
		t.Fatalf("NormalizeTools nested: %v", err)
	}
	got, err := NormalizeTools(flat, ToolFormatResponses, "tools")
	if err != nil {
		t.Fatalf("NormalizeTools flat: %v", err)
	}
	if len(want) != 1 || len(got) != 1 || !reflect.DeepEqual(want[0], got[0]) {
		t.Fatalf("normalized tools differ: nested=%#v flat=%#v", want, got)
	}
}

func TestFlattenToolOutputUsesSafeCompatibleText(t *testing.T) {
	raw := []byte(`[{"type":"output_text","text":"first"},{"type":"input_image","image_url":{"url":"https://example.test/x.png"}},{"type":"unknown_part","value":7}]`)
	got, err := FlattenToolOutput(raw, "output")
	if err != nil {
		t.Fatalf("FlattenToolOutput: %v", err)
	}
	want := "first\n\n[image omitted: unsupported by upstream]\n\n{\"type\":\"unknown_part\",\"value\":7}"
	if got != want {
		t.Fatalf("flattened output = %q, want %q", got, want)
	}
}

func TestResolveReasoningClampsToModelProfile(t *testing.T) {
	spec, err := ParseReasoning(map[string]json.RawMessage{"reasoning_effort": json.RawMessage(`"high"`)})
	if err != nil {
		t.Fatalf("ParseReasoning: %v", err)
	}
	decision, err := ResolveReasoning(spec, ReasoningProfile{
		Supported:      true,
		Levels:         []ReasoningLevel{ReasoningLow, ReasoningMedium},
		MinBudget:      512,
		MaxBudget:      8192,
		ZeroAllowed:    true,
		DynamicAllowed: false,
	})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if decision.RequestedLevel != ReasoningHigh || decision.EffectiveLevel != ReasoningMedium || decision.EffectiveBudget != 8192 {
		t.Fatalf("decision = %+v, want high -> medium/8192", decision)
	}
}

func TestApplyReasoningPreservesExplicitNativeThinking(t *testing.T) {
	fields := map[string]json.RawMessage{
		"thinking": json.RawMessage(`{"type":"enabled","budget_tokens":24576}`),
	}
	spec, err := ParseReasoning(fields)
	if err != nil {
		t.Fatalf("ParseReasoning: %v", err)
	}
	decision, err := ResolveReasoning(spec, ReasoningProfile{
		Supported: true, Levels: []ReasoningLevel{ReasoningLow, ReasoningMedium},
		MaxBudget: 8192, DynamicAllowed: false, WireFormat: "openai",
	})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if err := ApplyReasoning(fields, decision, ReasoningProfile{
		Supported: true, Levels: []ReasoningLevel{ReasoningLow, ReasoningMedium},
		MaxBudget: 8192, DynamicAllowed: false, WireFormat: "openai",
	}); err != nil {
		t.Fatalf("ApplyReasoning: %v", err)
	}
	if got := string(fields["thinking"]); got != `{"budget_tokens":8192,"type":"enabled"}` {
		t.Fatalf("thinking = %s, want native thinking object", got)
	}
	if _, ok := fields["reasoning_effort"]; ok {
		t.Fatal("reasoning_effort was synthesized for explicit native thinking")
	}
}

func TestParseReasoningRejectsConflictingAliases(t *testing.T) {
	_, err := ParseReasoning(map[string]json.RawMessage{
		"reasoning_effort": json.RawMessage(`"low"`),
		"thinking":         json.RawMessage(`{"type":"enabled","budget_tokens":8192}`),
	})
	if !errors.Is(err, ErrAmbiguousReasoning) {
		t.Fatalf("error = %v, want ErrAmbiguousReasoning", err)
	}
}

func TestResolveReasoningAutoPrefersAutoLevelOverNone(t *testing.T) {
	// Regression: an explicit auto request was mapped through budget distance
	// and collapsed to none (requestedBudget=-1 sits closest to 0) on dynamic
	// profiles. Auto must be preserved as the effective level.
	spec, err := ParseReasoning(map[string]json.RawMessage{"reasoning_effort": json.RawMessage(`"auto"`)})
	if err != nil {
		t.Fatalf("ParseReasoning: %v", err)
	}
	decision, err := ResolveReasoning(spec, ReasoningProfile{
		Supported: true, Levels: []ReasoningLevel{ReasoningNone, ReasoningAuto, ReasoningMedium, ReasoningHigh},
		ZeroAllowed: true, DynamicAllowed: true, WireFormat: "openai",
	})
	if err != nil {
		t.Fatalf("ResolveReasoning: %v", err)
	}
	if decision.EffectiveLevel != ReasoningAuto {
		t.Fatalf("EffectiveLevel = %q, want auto", decision.EffectiveLevel)
	}
	if decision.Downgraded {
		t.Fatal("explicit auto was marked as downgraded")
	}
}

func TestToolCallAccumulatorPairsStreamingArgumentsByIndex(t *testing.T) {
	var accumulator ToolCallAccumulator
	for _, delta := range []ToolCallDelta{
		{Index: 1, ID: "call-2", Name: "send", Arguments: `{"b":`},
		{Index: 0, ID: "call-1", Name: "lookup", Arguments: `{"a":`},
		{Index: 1, Arguments: `2}`},
		{Index: 0, Arguments: `1}`},
	} {
		if err := accumulator.Add(delta); err != nil {
			t.Fatalf("Add(%+v): %v", delta, err)
		}
	}
	got, err := accumulator.Calls()
	if err != nil {
		t.Fatalf("Calls: %v", err)
	}
	if len(got) != 2 || got[0].ID != "call-1" || got[0].Arguments != `{"a":1}` || got[1].ID != "call-2" || got[1].Arguments != `{"b":2}` {
		t.Fatalf("calls = %#v", got)
	}
}
