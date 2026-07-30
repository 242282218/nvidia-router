package responses

import (
	"reflect"
	"testing"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
)

func chatModel() modelcatalog.Model {
	return modelcatalog.Model{
		PublicID: "public-chat", UpstreamID: "vendor/chat", Kind: modelcatalog.KindChat, Enabled: true, ReasoningWireFormat: "openai", SupportsReasoning: true, SupportsTools: true,
	}
}

func wantTranscript(t *testing.T, body string, want []chatMessage) {
	t.Helper()
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	transcript, err := decodeTranscriptForTest(got)
	if err != nil {
		t.Fatalf("decode transcript: %v; got=%s", err, string(got))
	}
	if !reflect.DeepEqual(transcript.Messages, want) {
		t.Fatalf("transcript mismatch:\n got=%#v\nwant=%#v", transcript.Messages, want)
	}
}

func mustFail(t *testing.T, body, wantCode string) {
	t.Helper()
	_, err := ToChat([]byte(body), chatModel())
	if err == nil {
		t.Fatalf("expected error, got nil; body=%s", body)
	}
	publicError, ok := err.(*apierror.Error)
	if !ok {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	if publicError.Code != wantCode {
		t.Fatalf("code = %q, want %q; body=%s", publicError.Code, wantCode, body)
	}
	if publicError.Status != 400 {
		t.Fatalf("status = %d, want 400", publicError.Status)
	}
}

func TestToChatRequiresModel(t *testing.T) {
	mustFail(t, `{"input":"hi"}`, "missing_required_parameter")
}

func TestToChatRequiresInputOrInstructions(t *testing.T) {
	mustFail(t, `{"model":"public-chat"}`, "missing_required_parameter")
}

func TestToChatRejectsStoredResponses(t *testing.T) {
	mustFail(t, `{"model":"public-chat","store":true,"input":"hi"}`, "unsupported_responses_feature")
}

func TestToChatRejectsPreviousResponseID(t *testing.T) {
	mustFail(t, `{"model":"public-chat","previous_response_id":"resp_1","input":"hi"}`, "unsupported_responses_feature")
}

func TestToChatRejectsBackground(t *testing.T) {
	mustFail(t, `{"model":"public-chat","background":true,"input":"hi"}`, "unsupported_responses_feature")
}

func TestToChatRejectsFileInput(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":[{"type":"file"}]}`, "unsupported_responses_feature")
	mustFail(t, `{"model":"public-chat","input":[{"content":[{"type":"image_url"}]}]}`, "unsupported_responses_feature")
}

func TestToChatRejectsHostedTools(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":"hi","tools":[{"type":"web_search"}]}`, "unsupported_responses_feature")
	mustFail(t, `{"model":"public-chat","input":"hi","include":["file_search_call.searched_paths"]}`, "unsupported_responses_feature")
}

func TestToChatRejectsReplacesStateRecoveryFields(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":"hi","conversation":"resp_abc"}`, "unsupported_responses_feature")
}

func TestToChatTextInstructionsAndInput(t *testing.T) {
	wantTranscript(t, `{"model":"public-chat","instructions":"be brief","input":"hi"}`, []chatMessage{
		{Role: "system", RolePresent: true, Content: "be brief", ContentPresent: true},
		{Role: "user", RolePresent: true, Content: "hi", ContentPresent: true},
	})
}

func TestToChatStringInputBecomesSingleUser(t *testing.T) {
	wantTranscript(t, `{"model":"public-chat","input":"hello"}`, []chatMessage{
		{Role: "user", RolePresent: true, Content: "hello", ContentPresent: true},
	})
}

func TestToChatMultiturnInputPreservesOrder(t *testing.T) {
	body := `{"model":"public-chat","input":[
		{"role":"user","content":"ask"},
		{"role":"assistant","content":"answer"},
		{"role":"user","content":"again"}
	]}`
	wantTranscript(t, body, []chatMessage{
		{Role: "user", RolePresent: true, Content: "ask", ContentPresent: true},
		{Role: "assistant", RolePresent: true, Content: "answer", ContentPresent: true},
		{Role: "user", RolePresent: true, Content: "again", ContentPresent: true},
	})
}

func TestToChatFunctionCallBecomesAssistantToolCalls(t *testing.T) {
	body := `{"model":"public-chat","input":[
		{"type":"function_call","name":"get_weather","arguments":"{\"city\":\"NYC\"}","call_id":"fc_1"}
	]}`
	wantTranscript(t, body, []chatMessage{
		{
			Role: "assistant", RolePresent: true,
			ToolCalls: []chatToolCall{{Index: 0, ID: "fc_1", Type: "function", Function: chatFunction{Name: "get_weather", Arguments: `{"city":"NYC"}`}}},
		},
	})
}

func TestToChatFunctionCallOutputBecomesToolMessage(t *testing.T) {
	body := `{"model":"public-chat","input":[
		{"type":"function_call","name":"get_weather","arguments":"{}","call_id":"fc_1"},
		{"type":"function_call_output","call_id":"fc_1","output":"sunny"}
	]}`
	wantTranscript(t, body, []chatMessage{
		{Role: "assistant", RolePresent: true, ToolCalls: []chatToolCall{{Index: 0, ID: "fc_1", Type: "function", Function: chatFunction{Name: "get_weather", Arguments: `{}`}}}},
		{Role: "tool", RolePresent: true, ToolCallID: "fc_1", ToolCallIDPresent: true, Content: "sunny", ContentPresent: true},
	})
}

func TestToChatMapsToolsAndToolChoiceAndReasoning(t *testing.T) {
	body := `{"model":"public-chat","input":"x","tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":"auto","reasoning":{"effort":"high"},"max_output_tokens":128}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	for _, key := range []string{"tools", "tool_choice", "reasoning_effort", "max_tokens", "model"} {
		if !containsKey(t, got, key) {
			t.Fatalf("missing chat field %q; got=%s", key, string(got))
		}
	}
	if upstream := stringConcat(got); !stringContains(upstream, "vendor/chat") {
		t.Fatalf("upstream id not mapped; got=%s", string(got))
	}
}

func stringConcat(b []byte) string { return string(b) }

func TestToChatOmitsMaxTokensWhenUnused(t *testing.T) {
	body := `{"model":"public-chat","input":"x"}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	if containsKey(t, got, "max_tokens") {
		t.Fatalf("max_tokens should be absent when max_output_tokens omitted; got=%s", string(got))
	}
}

func TestToChatRejectsNonChatModelKind(t *testing.T) {
	embedding := modelcatalog.Model{PublicID: "public-chat", UpstreamID: "vendor/embed", Kind: modelcatalog.KindEmbedding, Enabled: true}
	_, err := ToChat([]byte(`{"model":"public-chat","input":"x"}`), embedding)
	if err == nil {
		t.Fatal("expected error for non-chat model")
	}
}

func containsKey(t *testing.T, payload []byte, key string) bool {
	t.Helper()
	return stringContains(string(payload), `"`+key+`"`)
}

func stringContains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
