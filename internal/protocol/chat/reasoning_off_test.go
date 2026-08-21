package chat

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

// plainChatModel is a model with no reasoning capability, the shape most NIM
// chat models have.
func plainChatModel() modelcatalog.Model {
	return modelcatalog.Model{
		PublicID: "public-model", UpstreamID: "public-model", Kind: modelcatalog.KindChat, Enabled: true,
		ReasoningWireFormat: "none",
	}
}

// Clients such as the OpenAI SDK wrappers send a reasoning parameter as a global
// default on every request. When it says "off", the model does not need the
// reasoning capability to honour it, so demanding one turned an ordinary request
// into 501 not_implemented for every non-reasoning model.
func TestParseDoesNotRequireReasoningCapabilityWhenReasoningIsOff(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"none"}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"off"}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"disabled"}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"thinking":false}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"}}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning":{"effort":"none"}}`,
	} {
		request, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("Parse(%s): %v", body, err)
		}
		if request.Requirements().Reasoning {
			t.Errorf("Parse(%s) demands the reasoning capability; reasoning is switched off", body)
		}
	}
}

func TestParseStillRequiresReasoningCapabilityWhenReasoningIsOn(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"low"}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"thinking":true}`,
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":8192}}`,
	} {
		request, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("Parse(%s): %v", body, err)
		}
		if !request.Requirements().Reasoning {
			t.Errorf("Parse(%s) does not demand the reasoning capability; reasoning is switched on", body)
		}
	}
}

// Letting reasoning-off requests through only helps if the now-meaningless alias
// never reaches the wire: NIM validates the chat schema strictly and answers 422
// for parameters the model does not declare, which would move the 501 to a 422
// instead of removing it.
func TestMarshalForDropsReasoningAliasesOnNonReasoningModel(t *testing.T) {
	for _, testCase := range []struct{ body, alias string }{
		{`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"none"}`, "reasoning_effort"},
		{`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"thinking":false}`, "thinking"},
		{`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning":{"effort":"none"}}`, "reasoning"},
	} {
		request, err := Parse([]byte(testCase.body))
		if err != nil {
			t.Fatalf("Parse(%s): %v", testCase.body, err)
		}
		encoded, err := request.MarshalFor(plainChatModel())
		if err != nil {
			t.Fatalf("MarshalFor(%s): %v", testCase.body, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatalf("decode body: %v; got=%s", err, encoded)
		}
		for _, name := range []string{"reasoning_effort", "reasoning", "thinking"} {
			if _, present := fields[name]; present {
				t.Errorf("MarshalFor(%s) forwarded %q to a non-reasoning model: %s", testCase.alias, name, encoded)
			}
		}
		if _, present := fields["messages"]; !present {
			t.Errorf("MarshalFor(%s) lost the messages field: %s", testCase.alias, encoded)
		}
	}
}

// A model that can reason must keep resolving the off switch through the normal
// wire-format path rather than having it silently stripped.
func TestMarshalForKeepsReasoningOffForReasoningModel(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"none"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	model := plainChatModel()
	model.SupportsReasoning = true
	model.ReasoningWireFormat = "openai"
	model.ReasoningLevels = []string{"none", "low", "medium"}
	model.ReasoningZeroAllowed = true
	encoded, err := request.MarshalFor(model)
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode body: %v; got=%s", err, encoded)
	}
	if got := string(fields["reasoning_effort"]); got != `"none"` {
		t.Fatalf("reasoning_effort = %s, want \"none\"", got)
	}
}
