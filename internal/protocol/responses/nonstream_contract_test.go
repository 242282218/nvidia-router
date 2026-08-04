package responses

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func decodeFromChat(t *testing.T, chatBody string) map[string]any {
	t.Helper()
	raw, err := FromChat([]byte(chatBody), "resp_contract", modelcatalog.Model{PublicID: "public-chat"})
	if err != nil {
		t.Fatalf("FromChat: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return decoded
}

// TestNonStreamCarriesCreatedAtAndOutputText locks in the two fields the SDKs
// read directly. output_text is derived from output, so a shape change in
// buildOutputItems would silently empty it without this assertion.
func TestNonStreamCarriesCreatedAtAndOutputText(t *testing.T) {
	decoded := decodeFromChat(t, `{"choices":[{"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop"}]}`)

	createdAt, ok := decoded["created_at"].(float64)
	if !ok || createdAt <= 0 {
		t.Fatalf("created_at = %#v, want positive unix seconds", decoded["created_at"])
	}
	if got := decoded["output_text"]; got != "Hello world" {
		t.Fatalf("output_text = %#v, want %q", got, "Hello world")
	}
	if got := decoded["status"]; got != "completed" {
		t.Fatalf("status = %#v, want completed", got)
	}
	if _, present := decoded["incomplete_details"]; present {
		t.Fatalf("incomplete_details must be absent for a completed response: %#v", decoded["incomplete_details"])
	}
}

// TestNonStreamTruncationReportsIncomplete covers the masked-truncation bug:
// finish_reason=length previously still reported status=completed, so callers
// could not tell a truncated answer from a whole one.
func TestNonStreamTruncationReportsIncomplete(t *testing.T) {
	decoded := decodeFromChat(t, `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`)

	if got := decoded["status"]; got != "incomplete" {
		t.Fatalf("status = %#v, want incomplete for finish_reason=length", got)
	}
	details, ok := decoded["incomplete_details"].(map[string]any)
	if !ok || details["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete_details = %#v, want reason max_output_tokens", decoded["incomplete_details"])
	}
}

func TestNonStreamContentFilterReportsIncomplete(t *testing.T) {
	decoded := decodeFromChat(t, `{"choices":[{"message":{"role":"assistant","content":"blocked"},"finish_reason":"content_filter"}]}`)

	if got := decoded["status"]; got != "incomplete" {
		t.Fatalf("status = %#v, want incomplete for finish_reason=content_filter", got)
	}
	details, ok := decoded["incomplete_details"].(map[string]any)
	if !ok || details["reason"] != "content_filter" {
		t.Fatalf("incomplete_details = %#v, want reason content_filter", decoded["incomplete_details"])
	}
}

// TestNonStreamToolCallsStayCompleted guards against over-reporting: a tool call
// is a normal completion, not a truncation.
func TestNonStreamToolCallsStayCompleted(t *testing.T) {
	decoded := decodeFromChat(t, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"fc_1","type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)

	if got := decoded["status"]; got != "completed" {
		t.Fatalf("status = %#v, want completed for finish_reason=tool_calls", got)
	}
	// No assistant message item, so output_text is legitimately empty.
	if got := decoded["output_text"]; got != "" {
		t.Fatalf("output_text = %#v, want empty for a tool-call-only response", got)
	}
}

// TestNonStreamOutputTextSpansMultipleParts verifies concatenation rather than
// first-part-only extraction.
func TestNonStreamOutputTextSpansMultipleParts(t *testing.T) {
	decoded := decodeFromChat(t, `{"choices":[{"message":{"role":"assistant","content":[{"type":"text","text":"Hel"},{"type":"text","text":"lo"}]},"finish_reason":"stop"}]}`)

	if got := decoded["output_text"]; got != "Hello" {
		t.Fatalf("output_text = %#v, want %q", got, "Hello")
	}
}
