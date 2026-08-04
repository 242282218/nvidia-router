package responses

import "testing"

// TestEveryEventPayloadCarriesType locks in that the JSON payload of every
// emitted event carries a "type" field equal to the SSE event name. The OpenAI
// SDKs dispatch on data.type, not on the SSE "event:" header, so a payload
// missing it is undispatchable and the endpoint is unusable from an official
// client even though the event stream looks correct on the wire.
func TestEveryEventPayloadCarriesType(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Reasoning: "thinking"},
		{Content: "hello"},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "search", Arguments: `{"q":1}`}}},
		{FinishReason: "tool_calls"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_type", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(emit.events) == 0 {
		t.Fatal("no events emitted")
	}
	for _, event := range emit.events {
		if event.Event == "done" {
			// The done event renders as the literal [DONE] marker, not JSON.
			continue
		}
		payload := event.Payload()
		if payload["type"] != event.Event {
			t.Fatalf("event %q payload type = %v, want %q", event.Event, payload["type"], event.Event)
		}
	}
}

// TestPayloadDoesNotMutateEventData guards the copy in Payload: emitting twice
// must not accumulate keys into the state machine's own map.
func TestPayloadDoesNotMutateEventData(t *testing.T) {
	event := EmittedEvent{Event: "response.created", Data: map[string]any{"sequence_number": 0}}
	_ = event.Payload()
	if _, exists := event.Data["type"]; exists {
		t.Fatal("Payload mutated the source Data map")
	}
}

// TestResponseLifecycleEventsNestResponseObject locks in that the response
// lifecycle events carry their fields inside a nested "response" object. SDKs
// read event.response.id/status/model; flattening those to the event root makes
// the response indistinguishable from an empty one.
func TestResponseLifecycleEventsNestResponseObject(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{{Content: "ok"}, {FinishReason: "stop"}}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_nest", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}

	lifecycle := map[string]string{
		"response.created":     "in_progress",
		"response.in_progress": "in_progress",
		"response.completed":   "completed",
	}
	seen := make(map[string]bool, len(lifecycle))
	for _, event := range emit.events {
		wantStatus, tracked := lifecycle[event.Event]
		if !tracked {
			continue
		}
		seen[event.Event] = true
		if _, flat := event.Data["id"]; flat {
			t.Fatalf("event %q still carries a flat id at the payload root", event.Event)
		}
		response, ok := event.Data["response"].(map[string]any)
		if !ok {
			t.Fatalf("event %q missing nested response object: %#v", event.Event, event.Data)
		}
		if response["id"] != "resp_nest" {
			t.Fatalf("event %q response.id = %v, want resp_nest", event.Event, response["id"])
		}
		if response["object"] != "response" {
			t.Fatalf("event %q response.object = %v, want response", event.Event, response["object"])
		}
		if response["model"] != "public-chat" {
			t.Fatalf("event %q response.model = %v, want public-chat", event.Event, response["model"])
		}
		if response["status"] != wantStatus {
			t.Fatalf("event %q response.status = %v, want %q", event.Event, response["status"], wantStatus)
		}
		if createdAt, ok := response["created_at"].(int64); !ok || createdAt <= 0 {
			t.Fatalf("event %q response.created_at = %v, want a positive unix timestamp", event.Event, response["created_at"])
		}
	}
	for name := range lifecycle {
		if !seen[name] {
			t.Fatalf("lifecycle event %q was never emitted", name)
		}
	}
}

// TestFailedTerminalNestsFailedStatus verifies the interrupted terminal reports
// status "failed" inside the nested response rather than the hardcoded
// "completed" the flat payload used to emit.
func TestFailedTerminalNestsFailedStatus(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{{Content: "partial"}}, end: ErrStreamInterrupted}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_fail", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, event := range emit.events {
		if event.Event != "response.failed" {
			continue
		}
		response, ok := event.Data["response"].(map[string]any)
		if !ok {
			t.Fatalf("response.failed missing nested response: %#v", event.Data)
		}
		if response["status"] != "failed" {
			t.Fatalf("response.failed status = %v, want failed", response["status"])
		}
		return
	}
	t.Fatal("response.failed was never emitted")
}

// TestTerminalCarriesAssembledOutput verifies the terminal event carries the
// assembled output array. SDKs read the final result from response.output
// instead of replaying deltas, so an absent output reads as an empty response.
func TestTerminalCarriesAssembledOutput(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Reasoning: "why"},
		{Content: "because"},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_9", Name: "lookup", Arguments: `{"k":2}`}}},
		{FinishReason: "tool_calls"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_out", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, event := range emit.events {
		if event.Event != "response.completed" {
			continue
		}
		response, _ := event.Data["response"].(map[string]any)
		output, ok := response["output"].([]any)
		if !ok {
			t.Fatalf("terminal response.output = %#v, want an array", response["output"])
		}
		// Output must be ordered by output_index: reasoning, message, tool.
		wantTypes := []string{"reasoning", "message", "function_call"}
		if len(output) != len(wantTypes) {
			t.Fatalf("output length = %d, want %d: %#v", len(output), len(wantTypes), output)
		}
		for i, wantType := range wantTypes {
			item, _ := output[i].(map[string]any)
			if item["type"] != wantType {
				t.Fatalf("output[%d] type = %v, want %q", i, item["type"], wantType)
			}
		}
		message, _ := output[1].(map[string]any)
		content, _ := message["content"].([]any)
		if len(content) != 1 {
			t.Fatalf("message content = %#v, want one part", content)
		}
		part, _ := content[0].(map[string]any)
		if part["text"] != "because" {
			t.Fatalf("message text = %v, want because", part["text"])
		}
		tool, _ := output[2].(map[string]any)
		if tool["arguments"] != `{"k":2}` || tool["name"] != "lookup" {
			t.Fatalf("tool item = %#v, want lookup with arguments", tool)
		}
		return
	}
	t.Fatal("response.completed was never emitted")
}

// TestSequenceNumbersAreZeroBased matches OpenAI's numbering, which starts at 0.
func TestSequenceNumbersAreZeroBased(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{{Content: "hi"}, {FinishReason: "stop"}}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_seq", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	first, ok := emit.events[0].Data["sequence_number"].(int)
	if !ok || first != 0 {
		t.Fatalf("first sequence_number = %v, want 0", emit.events[0].Data["sequence_number"])
	}
	assertMonoSeq(t, emit.events)
}
