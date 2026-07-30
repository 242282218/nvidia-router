package responses

import "fmt"

// ChatDelta is the parsed projection of a single Chat Completions streaming
// chunk that the Responses state machine consumes. The parser at the HTTP
// layer keeps only these fields; full text and arguments never persist.
type ChatDelta struct {
	Content      string
	Reasoning    string
	FinishReason string
	ToolCalls    []ChatToolCallDelta
}

// ChatToolCallDelta is one entry in a streaming choices[].delta.tool_calls
// array. Index identifies the parallel tool call across chunks.
type ChatToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// ChatDeltaSource feeds parsed deltas to the Responses state machine. EOF is
// signalled by returning ok=false with nil error. The source owns upstream
// decoding and cleanup; Convert only drives it forward.
type ChatDeltaSource interface {
	Next() (ChatDelta, error)
}

// Convert runs the Responses stream state machine over Chat deltas and emits
// the Responses event sequence to the emitter. It returns interrupted=true
// when upstream ended before the terminal [DONE], so the HTTP layer can decide
// between repair (when not yet committed) and a response.failed terminal.
// Convert never records prompt or completion content beyond the deltas it
// forwards inline.
func (s *streamState) Convert(source ChatDeltaSource, emit Emitter, responseID, model string) (interrupted bool, err error) {
	if err := emit.Emit(s.event("response.created", responseID, model)); err != nil {
		return false, fmt.Errorf("emit response.created: %w", err)
	}
	if err := emit.Emit(s.event("response.in_progress", responseID, model)); err != nil {
		return false, fmt.Errorf("emit response.in_progress: %w", err)
	}

	for {
		delta, srcErr := source.Next()
		if srcErr == ErrStreamInterrupted {
			// Upstream ended before the terminal [DONE] without finish_reason:
			// treated as an interruption unless the chat already signalled finish.
			if !s.finished {
				return true, s.finalize(emit, responseID, model, true)
			}
			return false, s.finalize(emit, responseID, model, false)
		}
		if srcErr != nil {
			return false, fmt.Errorf("read chat delta: %w", srcErr)
		}
		if err := s.applyDelta(delta, emit, responseID); err != nil {
			return false, err
		}
	}
}

func (s *streamState) applyDelta(delta ChatDelta, emit Emitter, responseID string) error {
	if delta.Reasoning != "" {
		if err := s.emitReasoningDelta(delta.Reasoning, emit, responseID); err != nil {
			return err
		}
	}
	if delta.Content != "" || (delta.FinishReason != "" && s.messageStarted && s.textPartOpen) {
		if err := s.emitTextDelta(delta.Content, emit, responseID); err != nil {
			return err
		}
	}
	for _, call := range delta.ToolCalls {
		if err := s.applyToolCall(call, emit, responseID); err != nil {
			return err
		}
	}
	if delta.FinishReason != "" {
		s.finished = true
	}
	return nil
}

func (s *streamState) emitTextDelta(text string, emit Emitter, responseID string) error {
	if !s.messageStarted {
		if err := s.openMessage(emit, responseID); err != nil {
			return err
		}
	}
	if text == "" {
		return nil
	}
	data := map[string]any{
		"sequence_number": s.nextSequence(),
		"delta":           text,
	}
	return emit.Emit(EmittedEvent{Event: "response.output_text.delta", Data: data})
}

func (s *streamState) openMessage(emit Emitter, responseID string) error {
	added := EmittedEvent{Event: "response.output_item.added", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    s.itemIndex,
		"item":            map[string]any{"type": "message", "role": "assistant"},
	}}
	if err := emit.Emit(added); err != nil {
		return fmt.Errorf("emit output_item.added: %w", err)
	}
	partAdded := EmittedEvent{Event: "response.content_part.added", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    s.itemIndex,
		"content_index":   0,
		"part":            map[string]any{"type": "output_text"},
	}}
	if err := emit.Emit(partAdded); err != nil {
		return fmt.Errorf("emit content_part.added: %w", err)
	}
	s.messageStarted = true
	s.textPartOpen = true
	return nil
}

func (s *streamState) emitReasoningDelta(text string, emit Emitter, responseID string) error {
	data := map[string]any{
		"sequence_number": s.nextSequence(),
		"delta":           map[string]any{"type": "summary_text", "text": text},
	}
	return emit.Emit(EmittedEvent{Event: "response.reasoning_summary_text.delta", Data: data})
}

func (s *streamState) applyToolCall(call ChatToolCallDelta, emit Emitter, responseID string) error {
	tool, exists := s.openTools[call.Index]
	if !exists {
		id := call.ID
		tool = &toolItem{outputIndex: s.itemIndex, id: id, name: call.Name}
		s.openTools[call.Index] = tool
		s.toolOrder = append(s.toolOrder, call.Index)
		added := EmittedEvent{Event: "response.output_item.added", Data: map[string]any{
			"sequence_number": s.nextSequence(),
			"output_index":    s.itemIndex,
			"item":            map[string]any{"type": "function_call", "id": id, "call_id": id, "name": call.Name},
		}}
		if err := emit.Emit(added); err != nil {
			return fmt.Errorf("emit tool output_item.added: %w", err)
		}
		s.itemIndex++
	}
	if call.Name != "" && tool.name == "" {
		tool.name = call.Name
	}
	if call.Arguments != "" {
		tool.arguments.write(call.Arguments)
		delta := EmittedEvent{Event: "response.function_call_arguments.delta", Data: map[string]any{
			"sequence_number": s.nextSequence(),
			"output_index":    tool.outputIndex,
			"item_id":         tool.id,
			"delta":           call.Arguments,
		}}
		if err := emit.Emit(delta); err != nil {
			return fmt.Errorf("emit arguments delta: %w", err)
		}
	}
	return nil
}

// finalize closes any open text part and tool items, emits the terminal and
// [DONE]. When interrupted, the terminal is response.failed so clients see a
// stable completion rather than a truncated success.
func (s *streamState) finalize(emit Emitter, responseID, model string, failed bool) error {
	if err := s.closeOpenMessage(emit); err != nil {
		return err
	}
	for _, idx := range s.toolOrder {
		tool := s.openTools[idx]
		if tool == nil || tool.closed {
			continue
		}
		if err := s.closeTool(tool, emit); err != nil {
			return err
		}
	}
	terminal := "response.completed"
	if failed {
		terminal = "response.failed"
	}
	data := map[string]any{
		"sequence_number": s.nextSequence(),
		"id":              responseID,
		"object":          "response",
		"status":          "completed",
	}
	if model != "" {
		data["model"] = model
	}
	if failed {
		data["status"] = "failed"
	}
	if err := emit.Emit(EmittedEvent{Event: terminal, Data: data}); err != nil {
		return fmt.Errorf("emit %s: %w", terminal, err)
	}
	done := EmittedEvent{Event: "done", Data: map[string]any{"sequence_number": s.nextSequence(), "done": true}}
	if err := emit.Emit(done); err != nil {
		return fmt.Errorf("emit done: %w", err)
	}
	return nil
}

func (s *streamState) closeOpenMessage(emit Emitter) error {
	if !s.messageStarted || !s.textPartOpen {
		return nil
	}
	textDone := EmittedEvent{Event: "response.output_text.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    s.itemIndex,
		"content_index":   0,
		"text":            "",
	}}
	if err := emit.Emit(textDone); err != nil {
		return fmt.Errorf("emit output_text.done: %w", err)
	}
	partDone := EmittedEvent{Event: "response.content_part.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    s.itemIndex,
		"content_index":   0,
		"part":            map[string]any{"type": "output_text"},
	}}
	if err := emit.Emit(partDone); err != nil {
		return fmt.Errorf("emit content_part.done: %w", err)
	}
	itemDone := EmittedEvent{Event: "response.output_item.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    s.itemIndex,
		"item":            map[string]any{"type": "message", "role": "assistant", "content": []any{}},
	}}
	if err := emit.Emit(itemDone); err != nil {
		return fmt.Errorf("emit output_item.done: %w", err)
	}
	s.itemIndex++
	s.textPartOpen = false
	return nil
}

func (s *streamState) closeTool(tool *toolItem, emit Emitter) error {
	arguments := tool.arguments.string()
	argsDone := EmittedEvent{Event: "response.function_call_arguments.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    tool.outputIndex,
		"item_id":         tool.id,
		"arguments":       arguments,
	}}
	if err := emit.Emit(argsDone); err != nil {
		return fmt.Errorf("emit arguments done: %w", err)
	}
	itemDone := EmittedEvent{Event: "response.output_item.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    tool.outputIndex,
		"item":            map[string]any{
			"type":      "function_call",
			"id":        tool.id,
			"call_id":   tool.id,
			"name":      tool.name,
			"arguments": arguments,
		},
	}}
	if err := emit.Emit(itemDone); err != nil {
		return fmt.Errorf("emit tool output_item.done: %w", err)
	}
	tool.closed = true
	return nil
}

// sentinel is a typed sentinel error so Convert can distinguish EOF from real
// errors without exposing a sentinel value across package boundaries.
type sentinelError struct{ msg string }

func (e sentinelError) Error() string { return e.msg }

func sentinel(msg string) error { return sentinelError{msg: msg} }
