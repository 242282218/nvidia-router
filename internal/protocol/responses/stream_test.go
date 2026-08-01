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
	last := 0
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
