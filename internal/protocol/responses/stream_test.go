package responses

import (
	"errors"
	"strings"
	"testing"
)

type fakeSource struct {
	deltas []ChatDelta
	i      int
	end    error
}

func (f *fakeSource) Next() (ChatDelta, error) {
	if f.i >= len(f.deltas) {
		if f.end != nil {
			return ChatDelta{}, f.end
		}
		return ChatDelta{}, ErrStreamCompleted
	}
	d := f.deltas[f.i]
	f.i++
	return d, nil
}

type failingSource struct{ err error }

func (f *failingSource) Next() (ChatDelta, error) { return ChatDelta{}, f.err }

type collectingEmitter struct {
	events    []EmittedEvent
	committed bool
	emitErr   error
}

func (c *collectingEmitter) Emit(event EmittedEvent) error {
	if c.emitErr != nil {
		return c.emitErr
	}
	c.events = append(c.events, event)
	return nil
}

func (c *collectingEmitter) Commit() error { c.committed = true; return nil }

func eventNames(events []EmittedEvent) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.Event)
	}
	return names
}

func assertMonoSeq(t *testing.T, events []EmittedEvent) {
	t.Helper()
	// Sequence numbers are 0-based, so the first event must be allowed to be 0.
	last := -1
	for _, e := range events {
		seq, ok := e.Data["sequence_number"].(int)
		if !ok {
			t.Fatalf("event %q missing int sequence_number: %#v", e.Event, e.Data)
		}
		if seq <= last {
			t.Fatalf("event %q sequence %d not greater than previous %d", e.Event, seq, last)
		}
		last = seq
	}
}

func terminalCount(events []EmittedEvent, name string) int {
	count := 0
	for _, e := range events {
		if e.Event == name {
			count++
		}
	}
	return count
}

func TestStreamTextThenToolUsesDistinctOutputIndices(t *testing.T) {
	// Text before a tool call must give the message and the function call
	// distinct output_index values, and the message done events must reference
	// the message index rather than the running counter.
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "let me check"},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "search"}}},
		{FinishReason: "tool_calls"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_m", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	messageAdded, toolAdded, messageDone := -1, -1, -1
	for _, e := range emit.events {
		switch e.Event {
		case "response.output_item.added":
			item, _ := e.Data["item"].(map[string]any)
			switch item["type"] {
			case "message":
				messageAdded, _ = e.Data["output_index"].(int)
			case "function_call":
				toolAdded, _ = e.Data["output_index"].(int)
			}
		case "response.output_item.done":
			item, _ := e.Data["item"].(map[string]any)
			if item["type"] == "message" {
				messageDone, _ = e.Data["output_index"].(int)
			}
		}
	}
	if messageAdded != 0 || toolAdded != 1 || messageDone != 0 {
		t.Fatalf("indices message_added=%d tool_added=%d message_done=%d, want 0/1/0", messageAdded, toolAdded, messageDone)
	}
	assertMonoSeq(t, emit.events)
}

func TestParseChatDeltaConcatenatesContentParts(t *testing.T) {
	data := `{"choices":[{"delta":{"content":[{"type":"text","text":"Hel"},{"type":"text","text":"lo"}]}}]}`
	delta, done, err := ParseChatDelta([]byte(data))
	if err != nil {
		t.Fatalf("ParseChatDelta: %v", err)
	}
	if done {
		t.Fatal("unexpected done marker")
	}
	if delta.Content != "Hello" {
		t.Fatalf("content = %q, want %q", delta.Content, "Hello")
	}
}

func TestParseChatDeltaIgnoresUntextContentParts(t *testing.T) {
	data := `{"choices":[{"delta":{"content":[{"type":"refusal","text":""},{"type":"text","text":"ok"}]}}]}`
	delta, _, err := ParseChatDelta([]byte(data))
	if err != nil {
		t.Fatalf("ParseChatDelta: %v", err)
	}
	if delta.Content != "ok" {
		t.Fatalf("content = %q, want %q", delta.Content, "ok")
	}
}

func TestParseChatDeltaPlainStringStillParses(t *testing.T) {
	data := `{"choices":[{"delta":{"content":"hi","reasoning_content":"think"}}]}`
	delta, _, err := ParseChatDelta([]byte(data))
	if err != nil {
		t.Fatalf("ParseChatDelta: %v", err)
	}
	if delta.Content != "hi" || delta.Reasoning != "think" {
		t.Fatalf("delta = %#v, want content hi reasoning think", delta)
	}
}

func TestParseChatDeltaReadsUsageOnlyChunk(t *testing.T) {
	delta, done, err := ParseChatDelta([]byte(`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`))
	if err != nil {
		t.Fatalf("ParseChatDelta: %v", err)
	}
	if done {
		t.Fatal("usage chunk was treated as done")
	}
	if delta.Usage == nil || delta.Usage.PromptTokens == nil || *delta.Usage.PromptTokens != 12 || delta.Usage.CompletionTokens == nil || *delta.Usage.CompletionTokens != 7 {
		t.Fatalf("usage = %#v, want prompt=12 completion=7", delta.Usage)
	}
}

func TestStreamTextEventSequence(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "He"},
		{Content: "llo"},
		{FinishReason: "stop"},
	}}
	emit := &collectingEmitter{}
	state := newStreamState()
	interrupted, err := state.Convert(source, emit, "resp_1", "public-chat")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if interrupted {
		t.Fatal("expected clean completion, got interrupted")
	}
	wantPrefix := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
		"done",
	}
	got := eventNames(emit.events)
	if len(got) != len(wantPrefix) {
		t.Fatalf("event sequence =\n%v\nwant starting with\n%v", got, wantPrefix)
	}
	for i, want := range wantPrefix {
		if got[i] != want {
			t.Fatalf("event[%d] = %q, want %q", i, got[i], want)
		}
	}
	if terminalCount(emit.events, "response.completed") != 1 {
		t.Fatalf("response.completed appeared %d times, want 1", terminalCount(emit.events, "response.completed"))
	}
	if terminalCount(emit.events, "response.failed") != 0 {
		t.Fatalf("response.failed appeared unexpectedly")
	}
	// text deltas carry forwarded content
	deltas := 0
	for _, e := range emit.events {
		if e.Event == "response.output_text.delta" {
			if e.Data["delta"] == "" {
				t.Fatalf("empty text delta payload: %#v", e.Data)
			}
			deltas++
		}
	}
	if deltas != 2 {
		t.Fatalf("text delta count = %d, want 2", deltas)
	}
	assertMonoSeq(t, emit.events)
}

func TestStreamCompletedEventIncludesUsage(t *testing.T) {
	prompt, completion, total := 12, 7, 19
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "ok"},
		{Usage: &ChatUsage{PromptTokens: &prompt, CompletionTokens: &completion, TotalTokens: &total}},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_usage", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, event := range emit.events {
		if event.Event != "response.completed" {
			continue
		}
		response, ok := event.Data["response"].(map[string]any)
		if !ok {
			t.Fatalf("completed event missing nested response: %#v", event.Data)
		}
		usage, ok := response["usage"].(map[string]any)
		if !ok || usage["input_tokens"] != 12 || usage["output_tokens"] != 7 || usage["total_tokens"] != 19 {
			t.Fatalf("completed usage = %#v, want input=12 output=7 total=19", response["usage"])
		}
		return
	}
	t.Fatal("response.completed event not emitted")
}

func TestStreamToolCallsParallelChunks(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "get"}, {Index: 1, ID: "fc_2", Name: "send"}}},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, Arguments: `{"a":`}, {Index: 1, Arguments: `{}}`}, {Index: 0, Arguments: `1}`}}},
		{FinishReason: "tool_calls"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_t", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	names := eventNames(emit.events)
	// Both tools announced before any arguments.deltas? No: deltas interleave
	// by upstream chunk, but both output_item.added precede their first delta.
	first := names[:4]
	want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.output_item.added"}
	for i, w := range want {
		if first[i] != w {
			t.Fatalf("prefix[%d] = %q, want %q; full=%v", i, first[i], w, names)
		}
	}
	// Each tool gets an arguments.done with full concatenated arguments.
	for i, e := range emit.events {
		if e.Event == "response.function_call_arguments.done" {
			args, _ := e.Data["arguments"].(string)
			if args == "" {
				t.Fatalf("tool[%d] arguments.done empty (expected concatenated)", i)
			}
		}
		if e.Event == "response.output_item.done" {
			item, _ := e.Data["item"].(map[string]any)
			if item["type"] == "function_call" {
				if item["call_id"] == "" || item["name"] == "" {
					t.Fatalf("tool output_item.done missing call fields: %#v", item)
				}
			}
		}
	}
	if terminalCount(emit.events, "response.completed") != 1 {
		t.Fatal("response.completed not emitted once")
	}
	assertMonoSeq(t, emit.events)
}

func TestStreamEmptyArgumentsAllowed(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "noop"}}},
		{FinishReason: "tool_calls"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_e", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	found := false
	for _, e := range emit.events {
		if e.Event == "response.function_call_arguments.done" {
			if e.Data["arguments"] != "" {
				t.Fatalf("empty tool arguments = %v, want empty string", e.Data["arguments"])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("arguments.done not emitted for tool with no argument chunks")
	}
	assertMonoSeq(t, emit.events)
}

func TestStreamToolArgumentsTotalCapped(t *testing.T) {
	// A hostile or broken upstream can stream argument fragments forever; the
	// accumulated total must be capped rather than growing without bound
	// (checklist #14).
	huge := strings.Repeat("a", maxToolArgumentsBytes+1)
	source := &fakeSource{deltas: []ChatDelta{
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "search", Arguments: huge}}},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_cap", "public-chat"); !errors.Is(err, ErrToolArgumentsTooLarge) {
		t.Fatalf("Convert error = %v, want ErrToolArgumentsTooLarge", err)
	}
}

func TestStreamToolArgumentsCappedAcrossFragments(t *testing.T) {
	// The cap must apply across the whole stream, not per fragment: many small
	// argument deltas that add up past the limit must fail too.
	half := maxToolArgumentsBytes / 2
	source := &fakeSource{deltas: []ChatDelta{
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "search", Arguments: strings.Repeat("a", half)}}},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, Arguments: strings.Repeat("b", half)}}},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, Arguments: "overflow"}}},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_cap2", "public-chat"); !errors.Is(err, ErrToolArgumentsTooLarge) {
		t.Fatalf("Convert error = %v, want ErrToolArgumentsTooLarge", err)
	}
}

func TestStreamEventsCarryCorrelationFields(t *testing.T) {
	// Every message/tool event must reference the same item via item_id and
	// carry the output_index/content_index pairs clients use to correlate
	// added/done lifecycles (OpenAI SDKs reject events with dangling indices).
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "hi"},
		{ToolCalls: []ChatToolCallDelta{{Index: 0, ID: "fc_1", Name: "search"}}},
		{FinishReason: "tool_calls"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_corr", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	var messageID, toolID string
	for _, e := range emit.events {
		switch e.Event {
		case "response.output_item.added":
			item, _ := e.Data["item"].(map[string]any)
			switch item["type"] {
			case "message":
				messageID, _ = item["id"].(string)
				if messageID == "" {
					t.Fatalf("message item missing id: %#v", item)
				}
			case "function_call":
				toolID, _ = item["id"].(string)
			}
		case "response.content_part.added", "response.content_part.done", "response.output_text.done":
			if e.Data["item_id"] != messageID {
				t.Fatalf("%s item_id = %v, want %q", e.Event, e.Data["item_id"], messageID)
			}
			if e.Data["output_index"] != 0 || e.Data["content_index"] != 0 {
				t.Fatalf("%s indices = output=%v content=%v, want 0/0", e.Event, e.Data["output_index"], e.Data["content_index"])
			}
		case "response.output_text.delta":
			if e.Data["item_id"] != messageID || e.Data["output_index"] != 0 || e.Data["content_index"] != 0 {
				t.Fatalf("output_text.delta correlation = %#v, want item %q output 0 content 0", e.Data, messageID)
			}
		case "response.output_item.done":
			switch e.Data["item_id"] {
			case messageID:
				if e.Data["output_index"] != 0 {
					t.Fatalf("message output_item.done index = %v, want 0", e.Data["output_index"])
				}
			case toolID:
				if e.Data["output_index"] != 1 {
					t.Fatalf("tool output_item.done index = %v, want 1", e.Data["output_index"])
				}
			default:
				t.Fatalf("output_item.done item_id = %v, want %q or %q", e.Data["item_id"], messageID, toolID)
			}
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if e.Data["item_id"] != toolID {
				t.Fatalf("%s item_id = %v, want %q", e.Event, e.Data["item_id"], toolID)
			}
		}
	}
	if messageID == "" || toolID == "" {
		t.Fatalf("message_id=%q tool_id=%q, want both set", messageID, toolID)
	}
}

func TestStreamReasoningMapsToSummary(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Reasoning: "thinking"},
		{Content: "answer"},
		{FinishReason: "stop"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_r", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	seenReasoningDelta := false
	for _, e := range emit.events {
		if e.Event == "response.reasoning_summary_text.delta" {
			seenReasoningDelta = true
			delta, _ := e.Data["delta"].(map[string]any)
			if delta == nil || delta["type"] != "summary_text" || delta["text"] != "thinking" {
				t.Fatalf("reasoning delta = %#v, want summary_text with text", delta)
			}
		}
	}
	if !seenReasoningDelta {
		t.Fatal("reasoning content did not map to a summary delta event")
	}
}

// TestStreamDoneEventsCarryAccumulatedText locks in the fix that the terminal
// output_text.done and output_item.done events carry the text accumulated
// across deltas. Before the fix the done events were empty, so SDKs that
// assemble the final message from done events produced empty output.
func TestStreamDoneEventsCarryAccumulatedText(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "Hel"},
		{Content: "lo "},
		{Content: "world"},
		{FinishReason: "stop"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_done", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	textDone := ""
	partDoneText := ""
	var itemContent []any
	seenTextDone, seenPartDone, seenItemDone := false, false, false
	for _, e := range emit.events {
		switch e.Event {
		case "response.output_text.done":
			seenTextDone = true
			textDone, _ = e.Data["text"].(string)
		case "response.content_part.done":
			seenPartDone = true
			part, _ := e.Data["part"].(map[string]any)
			if part != nil {
				partDoneText, _ = part["text"].(string)
			}
		case "response.output_item.done":
			item, _ := e.Data["item"].(map[string]any)
			if item["type"] == "message" {
				seenItemDone = true
				itemContent, _ = item["content"].([]any)
			}
		}
	}
	if !seenTextDone || !seenPartDone || !seenItemDone {
		t.Fatalf("done events missing; text_done=%v part_done=%v item_done=%v", seenTextDone, seenPartDone, seenItemDone)
	}
	if textDone != "Hello world" {
		t.Fatalf("output_text.done text = %q, want %q", textDone, "Hello world")
	}
	if partDoneText != "Hello world" {
		t.Fatalf("content_part.done part text = %q, want %q", partDoneText, "Hello world")
	}
	if len(itemContent) != 1 {
		t.Fatalf("output_item.done content = %#v, want one output_text part", itemContent)
	}
	part, _ := itemContent[0].(map[string]any)
	if part["type"] != "output_text" || part["text"] != "Hello world" {
		t.Fatalf("output_item.done part = %#v, want output_text with accumulated text", itemContent[0])
	}
}

// TestStreamReasoningItemLifecycle verifies the reasoning item has its own
// output_item.added/done lifecycle and every delta/done event carries the
// item_id/output_index/content_index correlation fields, mirroring how the
// message and tool items correlate so SDKs can assemble the item.
func TestStreamReasoningItemLifecycle(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Reasoning: "step one "},
		{Reasoning: "step two"},
		{Content: "answer"},
		{FinishReason: "stop"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_life", "public-chat"); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	addedIndex, doneIndex := -1, -1
	var reasoningID, summary string
	deltaEvents := 0
	for _, e := range emit.events {
		switch e.Event {
		case "response.output_item.added":
			item, _ := e.Data["item"].(map[string]any)
			if item["type"] == "reasoning" {
				reasoningID, _ = item["id"].(string)
				addedIndex, _ = e.Data["output_index"].(int)
			}
		case "response.reasoning_summary_text.delta":
			deltaEvents++
			if e.Data["item_id"] != reasoningID || e.Data["output_index"] != addedIndex || e.Data["content_index"] != 0 {
				t.Fatalf("reasoning delta correlation = %#v, want item %q output %d content 0", e.Data, reasoningID, addedIndex)
			}
		case "response.output_item.done":
			item, _ := e.Data["item"].(map[string]any)
			if item["type"] == "reasoning" {
				doneIndex, _ = e.Data["output_index"].(int)
				if s, ok := item["summary"].([]any); ok && len(s) > 0 {
					if entry, ok := s[0].(map[string]any); ok {
						summary, _ = entry["text"].(string)
					}
				}
			}
		}
	}
	if reasoningID == "" {
		t.Fatal("reasoning item added without id")
	}
	if addedIndex != 0 || doneIndex != 0 {
		t.Fatalf("reasoning indices added=%d done=%d, want 0/0 (message follows)", addedIndex, doneIndex)
	}
	if deltaEvents != 2 {
		t.Fatalf("reasoning delta count = %d, want 2", deltaEvents)
	}
	if summary != "step one step two" {
		t.Fatalf("reasoning done summary = %q, want accumulated text", summary)
	}
	assertMonoSeq(t, emit.events)
}

func TestStreamInterruptedBeforeFinishEmitsFailed(t *testing.T) {
	// Upstream ends without finish_reason while a message is mid-stream.
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "partial"},
		// no finish; fakeSource reports interruption after deltas exhausted
	}, end: ErrStreamInterrupted}
	emit := &collectingEmitter{}
	interrupted, err := newStreamState().Convert(source, emit, "resp_i", "public-chat")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !interrupted {
		t.Fatal("expected interrupted=true when upstream ends before finish")
	}
	if terminalCount(emit.events, "response.failed") != 1 {
		t.Fatalf("response.failed appeared %d times, want 1", terminalCount(emit.events, "response.failed"))
	}
	if terminalCount(emit.events, "response.completed") != 0 {
		t.Fatal("response.completed should not appear on interruption")
	}
	// Single [DONE] follows the terminal.
	if terminalCount(emit.events, "done") != 1 {
		t.Fatalf("done appeared %d times, want 1", terminalCount(emit.events, "done"))
	}
	assertMonoSeq(t, emit.events)
}

func TestStreamCleanDoneAfterFinishCompletes(t *testing.T) {
	// Upstream signals finish_reason and then emits [DONE].
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "hi"},
		{FinishReason: "stop"},
	}, end: ErrStreamCompleted}
	emit := &collectingEmitter{}
	interrupted, err := newStreamState().Convert(source, emit, "resp_c", "public-chat")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if interrupted {
		t.Fatal("expected clean completion after [DONE], got interrupted")
	}
	if terminalCount(emit.events, "response.completed") != 1 {
		t.Fatal("response.completed not emitted once")
	}
}

func TestStreamEOFAfterFinishStillFails(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "partial"},
		{FinishReason: "stop"},
	}, end: ErrStreamInterrupted}
	emit := &collectingEmitter{}
	interrupted, err := newStreamState().Convert(source, emit, "resp_eof", "public-chat")
	if err != nil || !interrupted {
		t.Fatalf("Convert = interrupted=%v err=%v, want interrupted EOF", interrupted, err)
	}
	if terminalCount(emit.events, "response.failed") != 1 || terminalCount(emit.events, "response.completed") != 0 {
		t.Fatalf("terminals = failed=%d completed=%d", terminalCount(emit.events, "response.failed"), terminalCount(emit.events, "response.completed"))
	}
}

func TestStreamDoneCompletesWithoutFinishReason(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{{Content: "complete"}}, end: ErrStreamCompleted}
	emit := &collectingEmitter{}
	interrupted, err := newStreamState().Convert(source, emit, "resp_done", "public-chat")
	if err != nil || interrupted {
		t.Fatalf("Convert = interrupted=%v err=%v, want clean completion", interrupted, err)
	}
	if terminalCount(emit.events, "response.completed") != 1 || terminalCount(emit.events, "response.failed") != 0 {
		t.Fatalf("terminals = completed=%d failed=%d", terminalCount(emit.events, "response.completed"), terminalCount(emit.events, "response.failed"))
	}
	if terminalCount(emit.events, "done") != 1 {
		t.Fatalf("done count = %d, want 1", terminalCount(emit.events, "done"))
	}
}

func TestStreamMalformedAfterDeltaEmitsFailedTerminal(t *testing.T) {
	expected := errors.New("malformed SSE")
	source := &failingSource{err: expected}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_x", "public-chat"); err == nil ||
		!strings.Contains(err.Error(), "malformed SSE") {
		t.Fatalf("Convert err = %v, want wrapped malformed SSE", err)
	}
	if terminalCount(emit.events, "response.failed") != 1 || terminalCount(emit.events, "done") != 1 {
		t.Fatalf("terminals = failed=%d done=%d, want one each", terminalCount(emit.events, "response.failed"), terminalCount(emit.events, "done"))
	}
	if terminalCount(emit.events, "response.completed") != 0 {
		t.Fatal("response.completed should not appear on malformed stream")
	}
}

func TestStreamLengthFinishReasonReportsIncomplete(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "partial answer"},
		{FinishReason: "length"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_x", "public-chat"); err != nil {
		t.Fatalf("Convert err = %v, want nil", err)
	}
	if terminalCount(emit.events, "response.incomplete") != 1 {
		t.Fatalf("response.incomplete count = %d, want 1; events=%v", terminalCount(emit.events, "response.incomplete"), eventNames(emit.events))
	}
	if terminalCount(emit.events, "response.completed") != 0 {
		t.Fatal("response.completed should not appear when finish_reason=length")
	}
	for _, e := range emit.events {
		if e.Event != "response.incomplete" {
			continue
		}
		response, ok := e.Data["response"].(map[string]any)
		if !ok {
			t.Fatalf("incomplete event missing nested response: %#v", e.Data)
		}
		if response["status"] != "incomplete" {
			t.Fatalf("status = %v, want incomplete", response["status"])
		}
		details, ok := response["incomplete_details"].(map[string]any)
		if !ok || details["reason"] != "max_output_tokens" {
			t.Fatalf("incomplete_details = %#v, want reason=max_output_tokens", response["incomplete_details"])
		}
	}
}

func TestStreamContentFilterFinishReasonReportsIncomplete(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "text"},
		{FinishReason: "content_filter"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_x", "public-chat"); err != nil {
		t.Fatalf("Convert err = %v, want nil", err)
	}
	if terminalCount(emit.events, "response.incomplete") != 1 {
		t.Fatalf("response.incomplete count = %d, want 1", terminalCount(emit.events, "response.incomplete"))
	}
}

func TestStreamStopFinishReasonStaysCompleted(t *testing.T) {
	source := &fakeSource{deltas: []ChatDelta{
		{Content: "full answer"},
		{FinishReason: "stop"},
	}}
	emit := &collectingEmitter{}
	if _, err := newStreamState().Convert(source, emit, "resp_x", "public-chat"); err != nil {
		t.Fatalf("Convert err = %v, want nil", err)
	}
	if terminalCount(emit.events, "response.completed") != 1 {
		t.Fatalf("response.completed count = %d, want 1; events=%v", terminalCount(emit.events, "response.completed"), eventNames(emit.events))
	}
	if terminalCount(emit.events, "response.incomplete") != 0 {
		t.Fatal("response.incomplete should not appear when finish_reason=stop")
	}
}
