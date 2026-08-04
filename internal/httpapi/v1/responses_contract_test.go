package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestResponsesStreamWireFormatCarriesTypeAndNestedResponse parses the bytes
// actually written to the client. The protocol-level tests cover the state
// machine, but the payload only reaches the wire through the emitter, so a
// regression there (marshalling Data instead of Payload) would leave every
// protocol test green while shipping payloads the OpenAI SDKs cannot dispatch.
func TestResponsesStreamWireFormatCarriesTypeAndNestedResponse(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	response, _ := serveStreamResponses(t, sseBody)

	frames := parseSSEFrames(t, response.Body.String())
	if len(frames) == 0 {
		t.Fatalf("no SSE frames parsed from:\n%s", response.Body.String())
	}

	var sawCreated, sawCompleted bool
	for _, frame := range frames {
		if frame.data == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(frame.data), &payload); err != nil {
			t.Fatalf("event %q data is not JSON: %v\n%s", frame.event, err, frame.data)
		}
		// The SDKs dispatch on payload.type, not on the event: header.
		if payload["type"] != frame.event {
			t.Fatalf("event %q payload type = %v, want it to match the event name", frame.event, payload["type"])
		}
		if _, ok := payload["sequence_number"]; !ok {
			t.Fatalf("event %q payload missing sequence_number: %s", frame.event, frame.data)
		}

		switch frame.event {
		case "response.created":
			sawCreated = true
			assertNestedResponse(t, frame.event, payload, "in_progress")
		case "response.completed":
			sawCompleted = true
			nested := assertNestedResponse(t, frame.event, payload, "completed")
			usage, ok := nested["usage"].(map[string]any)
			if !ok {
				t.Fatalf("response.completed missing nested usage: %s", frame.data)
			}
			if usage["input_tokens"] != float64(3) || usage["output_tokens"] != float64(2) {
				t.Fatalf("nested usage = %#v, want input=3 output=2", usage)
			}
			if _, ok := nested["output"].([]any); !ok {
				t.Fatalf("response.completed missing nested output array: %s", frame.data)
			}
		}
	}
	if !sawCreated || !sawCompleted {
		t.Fatalf("lifecycle events missing; created=%v completed=%v", sawCreated, sawCompleted)
	}
}

// TestResponsesStreamSequenceStartsAtZeroOnWire verifies the first frame on the
// wire numbers from 0, matching OpenAI. SDKs that track ordering by sequence
// treat a missing 0 as a dropped event.
func TestResponsesStreamSequenceStartsAtZeroOnWire(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	response, _ := serveStreamResponses(t, sseBody)

	frames := parseSSEFrames(t, response.Body.String())
	if len(frames) == 0 {
		t.Fatal("no SSE frames parsed")
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(frames[0].data), &first); err != nil {
		t.Fatalf("first frame not JSON: %v", err)
	}
	if first["sequence_number"] != float64(0) {
		t.Fatalf("first sequence_number = %v, want 0", first["sequence_number"])
	}
}

func assertNestedResponse(t *testing.T, event string, payload map[string]any, wantStatus string) map[string]any {
	t.Helper()
	nested, ok := payload["response"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no nested response object: %#v", event, payload)
	}
	if nested["object"] != "response" {
		t.Fatalf("%s nested object = %v, want \"response\"", event, nested["object"])
	}
	if nested["status"] != wantStatus {
		t.Fatalf("%s nested status = %v, want %q", event, nested["status"], wantStatus)
	}
	if _, ok := nested["id"].(string); !ok {
		t.Fatalf("%s nested response missing id: %#v", event, nested)
	}
	if _, ok := nested["created_at"]; !ok {
		t.Fatalf("%s nested response missing created_at: %#v", event, nested)
	}
	return nested
}

type sseFrame struct {
	event string
	data  string
}

// parseSSEFrames splits a recorded SSE body into event/data pairs, joining
// multi-line data fields the way an SSE client would.
func parseSSEFrames(t *testing.T, body string) []sseFrame {
	t.Helper()
	var frames []sseFrame
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var frame sseFrame
		var data []string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
		}
		if len(data) == 0 {
			continue
		}
		frame.data = strings.Join(data, "\n")
		frames = append(frames, frame)
	}
	return frames
}
