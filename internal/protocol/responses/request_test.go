package responses

import (
	"encoding/json"
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

func TestToChatAcceptsFlatResponsesFunctionTool(t *testing.T) {
	body := `{"model":"public-chat","input":"x","tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"type":"object","properties":{"key":{"type":"string"}}},"strict":true}]}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	var chat struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				Strict      *bool           `json:"strict"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(got, &chat); err != nil {
		t.Fatalf("decode Chat body: %v", err)
	}
	if len(chat.Tools) != 1 || chat.Tools[0].Type != "function" || chat.Tools[0].Function.Name != "lookup" || chat.Tools[0].Function.Description != "Look up a value" {
		t.Fatalf("flat tool was not normalized: %#v", chat.Tools)
	}
	if chat.Tools[0].Function.Strict == nil || !*chat.Tools[0].Function.Strict || string(chat.Tools[0].Function.Parameters) != `{"type":"object","properties":{"key":{"type":"string"}}}` {
		t.Fatalf("normalized function fields = %#v", chat.Tools[0].Function)
	}
}

func TestToChatAcceptsDeveloperAndInstructionsWithoutInput(t *testing.T) {
	body := `{"model":"public-chat","instructions":"follow this","input":[{"role":"developer","content":"be concise"}]}`
	wantTranscript(t, body, []chatMessage{
		{Role: "system", RolePresent: true, Content: "follow this", ContentPresent: true},
		{Role: "developer", RolePresent: true, Content: "be concise", ContentPresent: true},
	})

	wantTranscript(t, `{"model":"public-chat","instructions":"system only"}`, []chatMessage{
		{Role: "system", RolePresent: true, Content: "system only", ContentPresent: true},
	})
}

func TestToChatRejectsObjectFunctionOutput(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":[{"type":"function_call_output","call_id":"fc_1","output":{"value":"not a string"}}]}`, "invalid_parameter")
}

func TestToChatRejectsUnknownTopLevelField(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":"x","future_generation_mode":"fast"}`, "invalid_parameter")
}

func TestToChatConvertsNamedToolChoiceShape(t *testing.T) {
	body := `{"model":"public-chat","input":"x","tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":{"type":"function","name":"f"}}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	var chat map[string]json.RawMessage
	if err := json.Unmarshal(got, &chat); err != nil {
		t.Fatalf("decode chat body: %v", err)
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(chat["tool_choice"], &choice); err != nil {
		t.Fatalf("decode tool_choice: %v; got=%s", err, string(chat["tool_choice"]))
	}
	if choice.Type != "function" || choice.Function.Name != "f" {
		t.Fatalf("tool_choice = %#v, want function/f", choice)
	}
}

func TestToChatPassesThroughChatToolChoiceShape(t *testing.T) {
	body := `{"model":"public-chat","input":"x","tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":{"type":"function","function":{"name":"f"}}}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	if got := string(got); !stringContains(got, `{"name":"f"}`) {
		t.Fatalf("chat-shaped tool_choice was rewritten; got=%s", got)
	}
}

// nonReasoningModel mirrors chatModel but with reasoning disabled so that the
// mapReasoning capability gate is observable from tests.
func nonReasoningModel() modelcatalog.Model {
	m := chatModel()
	m.SupportsReasoning = false
	m.ReasoningWireFormat = "none"
	return m
}

func TestToChatPassesReasoningWithoutLocalCapabilityGate(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-chat","input":"hi","reasoning_effort":"high"}`,
		`{"model":"public-chat","input":"hi","reasoning":{"effort":"high"}}`,
	} {
		got, err := ToChat([]byte(body), nonReasoningModel())
		if err != nil {
			t.Fatalf("ToChat(%s): %v", body, err)
		}
		if !containsKey(t, got, "reasoning_effort") {
			t.Fatalf("reasoning_effort not forwarded; got=%s", string(got))
		}
	}
}

func TestToChatForwardsSamplingParameters(t *testing.T) {
	body := `{"model":"public-chat","input":"x","temperature":0,"top_p":0.5,"seed":42,"stop":["\n","END"],"presence_penalty":1,"frequency_penalty":0.2}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	var chat map[string]json.RawMessage
	if err := json.Unmarshal(got, &chat); err != nil {
		t.Fatalf("decode chat body: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "seed", "stop", "presence_penalty", "frequency_penalty"} {
		if _, ok := chat[key]; !ok {
			t.Fatalf("missing sampling field %q; got=%s", key, string(got))
		}
	}
	var temperature float64
	if err := json.Unmarshal(chat["temperature"], &temperature); err != nil || temperature != 0 {
		t.Fatalf("temperature = %v, want 0 forwarded (not default)", chat["temperature"])
	}
}

func TestToChatOmitsSamplingParametersWhenAbsent(t *testing.T) {
	body := `{"model":"public-chat","input":"x"}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "seed", "stop"} {
		if containsKey(t, got, key) {
			t.Fatalf("unexpected sampling field %q; got=%s", key, string(got))
		}
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

// TestToChatMapsTextFormatJSONObject locks in the fix that a Responses
// text.format json_object request is forwarded as a chat response_format.
// Without the mapping the structured-output request degrades to plain text
// with no error and the client parser fails downstream.
func TestToChatMapsTextFormatJSONObject(t *testing.T) {
	got, err := ToChat([]byte(`{"model":"public-chat","input":"x","text":{"format":{"type":"json_object"}}}`), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	var chat map[string]json.RawMessage
	if err := json.Unmarshal(got, &chat); err != nil {
		t.Fatalf("decode chat body: %v", err)
	}
	responseFormat, ok := chat["response_format"]
	if !ok {
		t.Fatalf("response_format missing; got=%s", string(got))
	}
	var format struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(responseFormat, &format); err != nil || format.Type != "json_object" {
		t.Fatalf("response_format = %s, want type json_object", responseFormat)
	}
}

// TestToChatMapsTextFormatJSONSchema forwards the schema and strict flag into
// the chat response_format object so the upstream enforces structured output.
func TestToChatMapsTextFormatJSONSchema(t *testing.T) {
	body := `{"model":"public-chat","input":"x","text":{"format":{"type":"json_schema","name":"step","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}}}}`
	got, err := ToChat([]byte(body), chatModel())
	if err != nil {
		t.Fatalf("ToChat: %v", err)
	}
	var chat map[string]json.RawMessage
	if err := json.Unmarshal(got, &chat); err != nil {
		t.Fatalf("decode chat body: %v", err)
	}
	responseFormat, ok := chat["response_format"]
	if !ok {
		t.Fatalf("response_format missing; got=%s", string(got))
	}
	var format struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict *bool           `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(responseFormat, &format); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if format.Type != "json_schema" || format.JSONSchema.Name != "step" ||
		format.JSONSchema.Strict == nil || !*format.JSONSchema.Strict {
		t.Fatalf("response_format = %s, want json_schema step strict=true", responseFormat)
	}
	if len(format.JSONSchema.Schema) == 0 || !stringContains(string(format.JSONSchema.Schema), `"answer"`) {
		t.Fatalf("schema not forwarded; response_format = %s", responseFormat)
	}
}

// TestToChatDefaultTextFormatIsNoOp keeps the default text format (and a
// plain string text parameter) from injecting a response_format.
func TestToChatDefaultTextFormatIsNoOp(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-chat","input":"x"}`,
		`{"model":"public-chat","input":"x","text":{"format":{"type":"text"}}}`,
		`{"model":"public-chat","input":"x","text":"inline"}`,
	} {
		got, err := ToChat([]byte(body), chatModel())
		if err != nil {
			t.Fatalf("ToChat(%s): %v", body, err)
		}
		if containsKey(t, got, "response_format") {
			t.Fatalf("response_format should be absent for default text format; body=%s got=%s", body, string(got))
		}
	}
}

func TestToChatRejectsUnknownTextFormat(t *testing.T) {
	// An unknown format is refused instead of being silently dropped so
	// structured-output requests never degrade to plain text.
	mustFail(t, `{"model":"public-chat","input":"x","text":{"format":{"type":"json_array"}}}`, "unsupported_responses_feature")
}

func TestToChatRejectsMalformedTextFormat(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":"x","text":{"format":42}}`, "invalid_parameter")
	mustFail(t, `{"model":"public-chat","input":"x","text":123}`, "invalid_parameter")
}

// TestToChatRejectsEmptyContentArray aligns the Responses path with chat
// semantics: empty user/system content produces no text and must be refused
// rather than silently accepted.
func TestToChatRejectsEmptyContentArray(t *testing.T) {
	mustFail(t, `{"model":"public-chat","input":[{"role":"user","content":[]}]}`, "invalid_parameter")
	mustFail(t, `{"model":"public-chat","input":[{"role":"user","content":[{"type":"input_text","text":""}]}]}`, "invalid_parameter")
}

func TestParseCarriesNormalizedResponseConfig(t *testing.T) {
	body := `{"model":"public-chat","instructions":"be brief","input":"x","stream":true,"parallel_tool_calls":false,"temperature":0,"top_p":0.5,"tools":[{"type":"function","name":"lookup","description":"Look up","parameters":{"type":"object"},"strict":true}],"tool_choice":{"type":"function","name":"lookup"}}`
	request, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if request.PublicModelID() != "public-chat" || !request.Stream() {
		t.Fatalf("request identity = %q/%v", request.PublicModelID(), request.Stream())
	}
	config := request.ResponseConfig()
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(config.Tools, &tools); err != nil || len(tools) != 1 {
		t.Fatalf("response tools = %s; err=%v", config.Tools, err)
	}
	if _, nested := tools[0]["function"]; nested || string(tools[0]["type"]) != `"function"` {
		t.Fatalf("response tool was not flattened: %#v", tools[0])
	}
	var choice map[string]json.RawMessage
	if err := json.Unmarshal(config.ToolChoice, &choice); err != nil || string(choice["name"]) != `"lookup"` {
		t.Fatalf("response tool choice = %s; err=%v", config.ToolChoice, err)
	}

	chat, err := request.MarshalFor(chatModel())
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(chat, &fields); err != nil {
		t.Fatalf("decode Chat request: %v", err)
	}
	var streamOptions map[string]bool
	if err := json.Unmarshal(fields["stream_options"], &streamOptions); err != nil || !streamOptions["include_usage"] {
		t.Fatalf("stream_options = %s; err=%v", fields["stream_options"], err)
	}
	var chatTools []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tools"], &chatTools); err != nil || len(chatTools) != 1 {
		t.Fatalf("Chat tools = %s; err=%v", fields["tools"], err)
	}
	if _, flatName := chatTools[0]["name"]; flatName {
		t.Fatalf("Chat tool still has flat name: %#v", chatTools[0])
	}
}

func TestParseAcceptsRouterReasoningOutputAsNoOp(t *testing.T) {
	body := `{"model":"public-chat","input":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"hidden"}]},{"type":"function_call","id":"fc_1","call_id":"fc_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"fc_1","output":"ok"}]}`
	request, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chat, err := request.MarshalFor(chatModel())
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	transcript, err := decodeTranscriptForTest(chat)
	if err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	if len(transcript.Messages) != 2 || transcript.Messages[0].Role != "assistant" || transcript.Messages[1].Role != "tool" {
		t.Fatalf("replayed transcript = %#v", transcript.Messages)
	}
	if transcript.Messages[0].ToolCalls[0].ID != "fc_1" || transcript.Messages[1].ToolCallID != "fc_1" {
		t.Fatalf("replayed tool linkage = %#v", transcript.Messages)
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

func TestToChatPassesUnknownReasoningEffortValues(t *testing.T) {
	for _, body := range []string{
		`{"model":"public-chat","input":"hi","reasoning_effort":"banana"}`,
		`{"model":"public-chat","input":"hi","reasoning":{"effort":"vendor-native"}}`,
	} {
		got, err := ToChat([]byte(body), chatModel())
		if err != nil {
			t.Fatalf("ToChat(%s): %v", body, err)
		}
		if !containsKey(t, got, "reasoning_effort") {
			t.Fatalf("reasoning_effort not forwarded; got=%s", string(got))
		}
	}
}
