package responses

import (
	"encoding/json"
	"testing"
)

// The Responses surface shares the reasoning parser with Chat, so it inherited
// the same defect: a reasoning parameter that says "off" demanded the reasoning
// capability and produced 501 not_implemented on every non-reasoning model.
func TestParseDoesNotRequireReasoningCapabilityWhenReasoningIsOff(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-chat","input":"hi","reasoning_effort":"none"}`,
		`{"model":"public-chat","input":"hi","reasoning":{"effort":"none"}}`,
		`{"model":"public-chat","input":"hi","thinking":false}`,
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
	request, err := Parse([]byte(`{"model":"public-chat","input":"hi","reasoning":{"effort":"high"}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !request.Requirements().Reasoning {
		t.Fatal("reasoning:{effort:high} must demand the reasoning capability")
	}
}

// mapReasoning deliberately forwards an active reasoning request to a model the
// local catalog marks as non-reasoning (see
// TestToChatPassesReasoningWithoutLocalCapabilityGate). An explicit off switch is
// the one case that must not be forwarded: it now passes the capability gate, and
// NIM answers 422 for chat parameters outside a model's schema, which would only
// move the 501 rather than remove it.
func TestToChatDropsReasoningAliasesWhenReasoningIsOffOnNonReasoningModel(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-chat","input":"hi","reasoning_effort":"none"}`,
		`{"model":"public-chat","input":"hi","reasoning":{"effort":"none"}}`,
		`{"model":"public-chat","input":"hi","thinking":false}`,
	} {
		encoded, err := ToChat([]byte(body), nonReasoningModel())
		if err != nil {
			t.Fatalf("ToChat(%s): %v", body, err)
		}
		var chat map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &chat); err != nil {
			t.Fatalf("decode body: %v; got=%s", err, encoded)
		}
		for _, name := range []string{"reasoning_effort", "reasoning", "thinking"} {
			if _, present := chat[name]; present {
				t.Errorf("ToChat(%s) forwarded %q to a non-reasoning model: %s", body, name, encoded)
			}
		}
		if _, present := chat["messages"]; !present {
			t.Errorf("ToChat(%s) lost the messages field: %s", body, encoded)
		}
	}
}
