package chat

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

// Auto-injection defaults to the strongest level no heavier than "medium":
// a request that never mentioned reasoning must not silently get the slowest
// path (and vocabularies that stop below max stay in range). For this
// none/low/high/max profile that is low.
func TestMarshalForWithOptionsInjectsModerateReasoning(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public/model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	model := modelcatalog.Model{
		PublicID:             "public/model",
		UpstreamID:           "upstream/model",
		Kind:                 modelcatalog.KindChat,
		Enabled:              true,
		SupportsReasoning:    true,
		ReasoningWireFormat:  "openai",
		ReasoningLevels:      []string{"none", "low", "high", "max"},
		ReasoningZeroAllowed: true,
	}
	body, err := request.MarshalForWithOptions(model, true)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	var effort string
	if err := json.Unmarshal(fields["reasoning_effort"], &effort); err != nil {
		t.Fatal(err)
	}
	if effort != "low" {
		t.Fatalf("reasoning_effort = %q, want low", effort)
	}
}

// A tiny max_tokens window gets reasoning switched off instead of a level the
// upstream would spend the entire allowance on (hy3/kimi-k3 answered with an
// empty content and finish_reason=length at 64 tokens).
func TestMarshalForWithOptionsInjectsNoneUnderTinyMaxTokens(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public/model","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`))
	if err != nil {
		t.Fatal(err)
	}
	model := modelcatalog.Model{
		PublicID:             "public/model",
		UpstreamID:           "upstream/model",
		Kind:                 modelcatalog.KindChat,
		Enabled:              true,
		SupportsReasoning:    true,
		ReasoningWireFormat:  "openai",
		ReasoningLevels:      []string{"none", "low", "high", "max"},
		ReasoningZeroAllowed: true,
	}
	body, err := request.MarshalForWithOptions(model, true)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	var effort string
	if err := json.Unmarshal(fields["reasoning_effort"], &effort); err != nil {
		t.Fatal(err)
	}
	if effort != "none" {
		t.Fatalf("reasoning_effort = %q, want none", effort)
	}
}

// A model that cannot express "off" (none listed but ZeroAllowed=false) must
// not receive any injected level on a tiny window: silence beats starvation,
// and no spec is ever constructed so the 501 path stays unreachable.
func TestMarshalForWithOptionsSkipsInjectionWhenZeroUnavailable(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public/model","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`))
	if err != nil {
		t.Fatal(err)
	}
	model := modelcatalog.Model{
		PublicID:            "public/model",
		UpstreamID:          "upstream/model",
		Kind:                modelcatalog.KindChat,
		Enabled:             true,
		SupportsReasoning:   true,
		ReasoningWireFormat: "openai",
		ReasoningLevels:     []string{"none"},
	}
	body, err := request.MarshalForWithOptions(model, true)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reasoning_effort", "reasoning", "thinking"} {
		if _, present := fields[name]; present {
			t.Fatalf("injected %q into a tiny-window request for a profile without none", name)
		}
	}
}

func TestMarshalForWithOptionsPreservesExplicitReasoning(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public/model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"low"}`))
	if err != nil {
		t.Fatal(err)
	}
	model := modelcatalog.Model{
		PublicID:             "public/model",
		UpstreamID:           "upstream/model",
		Kind:                 modelcatalog.KindChat,
		Enabled:              true,
		SupportsReasoning:    true,
		ReasoningWireFormat:  "openai",
		ReasoningLevels:      []string{"none", "low", "high", "max"},
		ReasoningZeroAllowed: true,
	}
	body, err := request.MarshalForWithOptions(model, true)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	var effort string
	if err := json.Unmarshal(fields["reasoning_effort"], &effort); err != nil {
		t.Fatal(err)
	}
	if effort != "low" {
		t.Fatalf("reasoning_effort = %q, want explicit low", effort)
	}
}
