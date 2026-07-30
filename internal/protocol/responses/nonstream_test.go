package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func nonstreamModel() modelcatalog.Model {
	return modelcatalog.Model{PublicID: "public-chat", UpstreamID: "vendor/chat", Kind: modelcatalog.KindChat, Enabled: true}
}

func decodeResponses(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode responses: %v; body=%s", err, string(body))
	}
	return result
}

func TestFromChatTextResponse(t *testing.T) {
	chat := `{"id":"c1","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	got, err := FromChat([]byte(chat), "resp_abc", nonstreamModel())
	if err != nil {
		t.Fatalf("FromChat: %v", err)
	}
	result := decodeResponses(t, got)

	if result["id"] != "resp_abc" {
		t.Fatalf("id = %v, want resp_abc", result["id"])
	}
	if result["object"] != "response" {
		t.Fatalf("object = %v, want response", result["object"])
	}
	if result["status"] != "completed" {
		t.Fatalf("status = %v, want completed", result["status"])
	}
	if result["model"] != "public-chat" {
		t.Fatalf("model = %v, want public-chat", result["model"])
	}

	output, _ := result["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output len = %d, want 1", len(output))
	}
	item := output[0].(map[string]any)
	if item["type"] != "message" {
		t.Fatalf("output type = %v, want message", item["type"])
	}
	content := item["content"].([]any)
	if content[0].(map[string]any)["type"] != "output_text" {
		t.Fatalf("content type = %v, want output_text", content[0].(map[string]any)["type"])
	}
	if content[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("text = %v, want hello", content[0].(map[string]any)["text"])
	}
}

func TestFromChatToolCallsPreserveOrder(t *testing.T) {
	chat := `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"fc_1","type":"function","function":{"name":"get","arguments":"{\"a\":1}"}},{"id":"fc_2","type":"function","function":{"name":"send","arguments":"{}"}}]}}]}`
	got, err := FromChat([]byte(chat), "resp_1", nonstreamModel())
	if err != nil {
		t.Fatalf("FromChat: %v", err)
	}
	result := decodeResponses(t, got)
	output, _ := result["output"].([]any)
	if len(output) != 2 && len(output) != 3 {
		t.Fatalf("output len = %d, want tool items (+optional empty text)", len(output))
	}
	first := output[0].(map[string]any)
	if first["type"] != "function_call" {
		t.Fatalf("first type = %v, want function_call", first["type"])
	}
	if first["name"] != "get" {
		t.Fatalf("first name = %v, want get", first["name"])
	}
	if first["arguments"] != `{"a":1}` {
		t.Fatalf("first args = %v", first["arguments"])
	}
}

func TestFromChatUsageMapping(t *testing.T) {
	chat := `{"choices":[{"message":{"role":"assistant","content":"x"}}],"usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17}}`
	got, err := FromChat([]byte(chat), "resp_u", nonstreamModel())
	if err != nil {
		t.Fatalf("FromChat: %v", err)
	}
	result := decodeResponses(t, got)
	usage := result["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) {
		t.Fatalf("input_tokens = %v, want 10", usage["input_tokens"])
	}
	if usage["output_tokens"] != float64(7) {
		t.Fatalf("output_tokens = %v, want 7", usage["output_tokens"])
	}
	if usage["total_tokens"] != float64(17) {
		t.Fatalf("total_tokens = %v, want 17", usage["total_tokens"])
	}
}

func TestFromChatOmitsUsageWhenAbsent(t *testing.T) {
	chat := `{"choices":[{"message":{"role":"assistant","content":"x"}}]}`
	got, err := FromChat([]byte(chat), "resp_n", nonstreamModel())
	if err != nil {
		t.Fatalf("FromChat: %v", err)
	}
	result := decodeResponses(t, got)
	if _, ok := result["usage"]; ok {
		t.Fatalf("usage should be absent; body=%s", string(got))
	}
}

func TestFromChatRejectsEmptyChoices(t *testing.T) {
	chat := `{"choices":[]}`
	if _, err := FromChat([]byte(chat), "resp_e", nonstreamModel()); err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestFromChatRejectsMissingID(t *testing.T) {
	chat := `{"choices":[{"message":{"role":"assistant","content":"x"}}]}`
	if _, err := FromChat([]byte(chat), "  ", nonstreamModel()); err == nil {
		t.Fatal("expected error for missing responses id")
	}
}

func TestFromChatUsesPublicModelNotUpstream(t *testing.T) {
	chat := `{"choices":[{"message":{"role":"assistant","content":"x"}}]}`
	got, err := FromChat([]byte(chat), "resp_m", nonstreamModel())
	if err != nil {
		t.Fatalf("FromChat: %v", err)
	}
	if strings.Contains(string(got), "vendor/chat") {
		t.Fatalf("upstream id leaked into responses: %s", string(got))
	}
}
