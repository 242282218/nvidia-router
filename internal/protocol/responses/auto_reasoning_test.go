package responses

import (
	"encoding/json"
	"fmt"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

// Auto-injection defaults to the strongest level no heavier than "medium"
// (see internal/compat.AutoReasoningSpec): for this none/low/high/max
// profile the injected effort is low, never the heaviest level.
func TestMarshalForWithOptionsInjectsReasoningIntoChatPayload(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public/model","input":"hi"}`))
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

// max_output_tokens is renamed to max_tokens before the injection ladder runs;
// a tiny Responses allowance must suppress reasoning exactly like chat.
func TestMarshalForWithOptionsHonorsMaxOutputTokensInLadder(t *testing.T) {
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
	readEffort := func(t *testing.T, limit int) string {
		t.Helper()
		body := fmt.Sprintf(`{"model":"public/model","input":"hi","max_output_tokens":%d}`, limit)
		request, err := Parse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := request.MarshalForWithOptions(model, true)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		var effort string
		if err := json.Unmarshal(fields["reasoning_effort"], &effort); err != nil {
			t.Fatalf("decode effort from %s: %v", encoded, err)
		}
		return effort
	}
	if got := readEffort(t, 64); got != "none" {
		t.Fatalf("max_output_tokens=64: reasoning_effort = %q, want none", got)
	}
	if got := readEffort(t, 4000); got != "low" {
		t.Fatalf("max_output_tokens=4000: reasoning_effort = %q, want low", got)
	}
}
