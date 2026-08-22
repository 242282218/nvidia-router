package chat

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func TestMarshalForWithOptionsInjectsHighestReasoning(t *testing.T) {
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
	if effort != "max" {
		t.Fatalf("reasoning_effort = %q, want max", effort)
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
