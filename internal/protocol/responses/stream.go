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
	Usage        *ChatUsage
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
// the Responses event sequence to the emitter. ErrStreamCompleted is the only
// normal completion signal; EOF/interruption before [DONE] returns
// interrupted=true and emits response.failed. Other source errors are wrapped
// after emitting one failed terminal so a committed client stream is closed
// without triggering another key attempt.
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
		switch srcErr {
		case nil:
			if err := s.applyDelta(delta, emit, responseID); err != nil {
				// Same invariant as the default branch below: the response is
				// already committed, so a conversion failure must still emit a
				// terminal event. Returning bare left the client waiting on
				// response.completed and [DONE] until its idle timeout. finalize is
				// idempotent via s.finalized, so no duplicate terminal is possible.
				finalErr := s.finalize(emit, responseID, model, true)
				if finalErr != nil {
					return false, fmt.Errorf("apply chat delta: %w; finalize stream: %v", err, finalErr)
				}
				return false, fmt.Errorf("apply chat delta: %w", err)
			}
		case ErrStreamCompleted:
			return false, s.finalize(emit, responseID, model, false)
		case ErrStreamInterrupted:
			// EOF/interruption before [DONE] is failed, even if the upstream sent
			// finish_reason. A finish_reason is only a delta-level signal; [DONE]
			// is the authoritative normal completion marker.
			return true, s.finalize(emit, responseID, model, true)
		default:
			// Once the first event has committed the response, do not leave the
			// client with an unterminated stream and never allow a second key
			// attempt. Emit one failed terminal and preserve the read error.
			finalErr := s.finalize(emit, responseID, model, true)
			if finalErr != nil {
				return false, fmt.Errorf("read chat delta: %w; finalize stream: %v", srcErr, finalErr)
			}
			return false, fmt.Errorf("read chat delta: %w", srcErr)
		}
	}
}

func (s *streamState) applyDelta(delta ChatDelta, emit Emitter, responseID string) error {
	if delta.Usage != nil {
		s.usage = delta.Usage
	}
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
		// Keep the most specific signal: later finish_reason values supersede
		// earlier empty ones, and a terminal length/content_filter must survive
		// to finalize so truncation is reported as incomplete.
		s.finishReason = delta.FinishReason
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
	if err := s.text.write(text); err != nil {
		return fmt.Errorf("accumulate assistant text: %w", err)
	}
	data := map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.messageID,
		"output_index":    s.messageIndex,
		"content_index":   0,
		"delta":           text,
	}
	return emit.Emit(EmittedEvent{Event: "response.output_text.delta", Data: data})
}

func (s *streamState) openMessage(emit Emitter, responseID string) error {
	s.messageIndex = s.itemIndex
	s.messageID = messageItemID(responseID)
	added := EmittedEvent{Event: "response.output_item.added", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    s.messageIndex,
		"item":            map[string]any{"id": s.messageID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
	}}
	if err := emit.Emit(added); err != nil {
		return fmt.Errorf("emit output_item.added: %w", err)
	}
	partAdded := EmittedEvent{Event: "response.content_part.added", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.messageID,
		"output_index":    s.messageIndex,
		"content_index":   0,
		"part":            map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}},
	}}
	if err := emit.Emit(partAdded); err != nil {
		return fmt.Errorf("emit content_part.added: %w", err)
	}
	// Reserve this index for the message item so a later tool call cannot
	// collide with it and message done events reference the same index.
	s.itemIndex++
	s.messageStarted = true
	s.textPartOpen = true
	return nil
}

func (s *streamState) emitReasoningDelta(text string, emit Emitter, responseID string) error {
	if !s.reasoningOpen {
		s.reasoningOpen = true
		s.reasoningIndex = s.itemIndex
		s.reasoningID = reasoningItemID(responseID)
		added := EmittedEvent{Event: "response.output_item.added", Data: map[string]any{
			"sequence_number": s.nextSequence(),
			"output_index":    s.reasoningIndex,
			"item":            map[string]any{"id": s.reasoningID, "type": "reasoning", "status": "in_progress", "summary": []any{}},
		}}
		if err := emit.Emit(added); err != nil {
			return fmt.Errorf("emit reasoning output_item.added: %w", err)
		}
		s.itemIndex++
	}
	if err := s.reasoning.write(text); err != nil {
		return fmt.Errorf("accumulate reasoning summary: %w", err)
	}
	data := map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.reasoningID,
		"output_index":    s.reasoningIndex,
		"summary_index":   0,
		"delta":           text,
	}
	return emit.Emit(EmittedEvent{Event: "response.reasoning_summary_text.delta", Data: data})
}

func (s *streamState) applyToolCall(call ChatToolCallDelta, emit Emitter, responseID string) error {
	tool, exists := s.openTools[call.Index]
	if !exists {
		id := call.ID
		tool = &toolItem{
			outputIndex: s.itemIndex,
			id:          id,
			name:        call.Name,
			arguments:   newBoundedBuilder(maxToolArgumentsBytes, ErrToolArgumentsTooLarge),
		}
		s.openTools[call.Index] = tool
		s.toolOrder = append(s.toolOrder, call.Index)
		added := EmittedEvent{Event: "response.output_item.added", Data: map[string]any{
			"sequence_number": s.nextSequence(),
			"output_index":    s.itemIndex,
			"item":            map[string]any{"type": "function_call", "status": "in_progress", "id": id, "call_id": id, "name": call.Name, "arguments": ""},
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
		if err := tool.arguments.write(call.Arguments); err != nil {
			return fmt.Errorf("accumulate tool call %d arguments: %w", call.Index, err)
		}
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
	if s.finalized {
		return nil
	}
	s.finalized = true
	// Close items in output_index order so done events correlate with the order
	// clients saw items added.
	if s.reasoningOpen && (!s.messageStarted || s.reasoningIndex < s.messageIndex) {
		if err := s.closeReasoningItem(emit); err != nil {
			return err
		}
		if err := s.closeOpenMessage(emit); err != nil {
			return err
		}
	} else {
		if err := s.closeOpenMessage(emit); err != nil {
			return err
		}
		if err := s.closeReasoningItem(emit); err != nil {
			return err
		}
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
	status := "completed"
	if failed {
		terminal = "response.failed"
		status = "failed"
	} else if reason, ok := incompleteReason(s.finishReason); ok {
		// A delta-level finish_reason of length/content_filter means the upstream
		// truncated the response (max_output_tokens, content filter). Report it as
		// incomplete — the same semantics the non-stream path applies — so clients
		// that gate on response.status do not mistake truncation for a full answer.
		terminal = "response.incomplete"
		status = "incomplete"
		response := s.responseObject(responseID, model, status)
		response["incomplete_details"] = map[string]any{"reason": reason}
		response["output"] = s.outputItems()
		response["output_text"] = s.text.string()
		if usage := convertUsage(s.usage); usage != nil {
			response["usage"] = usage
		}
		data := map[string]any{
			"sequence_number": s.nextSequence(),
			"response":        response,
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
	response := s.responseObject(responseID, model, status)
	// SDKs read the assembled result from response.output on the terminal event
	// rather than replaying every delta, so an absent output reads as an empty
	// response even when the deltas carried content.
	response["output"] = s.outputItems()
	response["output_text"] = s.text.string()
	if !failed {
		if usage := convertUsage(s.usage); usage != nil {
			response["usage"] = usage
		}
	}
	data := map[string]any{
		"sequence_number": s.nextSequence(),
		"response":        response,
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
	text := s.text.string()
	textDone := EmittedEvent{Event: "response.output_text.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.messageID,
		"output_index":    s.messageIndex,
		"content_index":   0,
		"text":            text,
	}}
	if err := emit.Emit(textDone); err != nil {
		return fmt.Errorf("emit output_text.done: %w", err)
	}
	part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
	partDone := EmittedEvent{Event: "response.content_part.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.messageID,
		"output_index":    s.messageIndex,
		"content_index":   0,
		"part":            part,
	}}
	if err := emit.Emit(partDone); err != nil {
		return fmt.Errorf("emit content_part.done: %w", err)
	}
	content := []any{}
	if text != "" {
		content = []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}}
	}
	itemDone := EmittedEvent{Event: "response.output_item.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.messageID,
		"output_index":    s.messageIndex,
		"item":            map[string]any{"id": s.messageID, "type": "message", "status": "completed", "role": "assistant", "content": content},
	}}
	if err := emit.Emit(itemDone); err != nil {
		return fmt.Errorf("emit output_item.done: %w", err)
	}
	s.textPartOpen = false
	return nil
}

// closeReasoningItem terminates an open reasoning item with the accumulated
// summary, mirroring the message/tool output_item.done lifecycle. Without it
// clients that assemble output from done events would lose the reasoning text.
func (s *streamState) closeReasoningItem(emit Emitter) error {
	if !s.reasoningOpen {
		return nil
	}
	s.reasoningOpen = false
	summary := s.reasoning.string()
	textDone := EmittedEvent{Event: "response.reasoning_summary_text.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.reasoningID,
		"output_index":    s.reasoningIndex,
		"summary_index":   0,
		"text":            summary,
	}}
	if err := emit.Emit(textDone); err != nil {
		return fmt.Errorf("emit reasoning_summary_text.done: %w", err)
	}
	itemDone := EmittedEvent{Event: "response.output_item.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         s.reasoningID,
		"output_index":    s.reasoningIndex,
		"item": map[string]any{
			"id":      s.reasoningID,
			"type":    "reasoning",
			"status":  "completed",
			"summary": []any{map[string]any{"type": "summary_text", "text": summary}},
		},
	}}
	if err := emit.Emit(itemDone); err != nil {
		return fmt.Errorf("emit reasoning output_item.done: %w", err)
	}
	return nil
}

func (s *streamState) closeTool(tool *toolItem, emit Emitter) error {
	arguments := tool.arguments.string()
	argsDone := EmittedEvent{Event: "response.function_call_arguments.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"output_index":    tool.outputIndex,
		"item_id":         tool.id,
		"name":            tool.name,
		"arguments":       arguments,
	}}
	if err := emit.Emit(argsDone); err != nil {
		return fmt.Errorf("emit arguments done: %w", err)
	}
	itemDone := EmittedEvent{Event: "response.output_item.done", Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"item_id":         tool.id,
		"output_index":    tool.outputIndex,
		"item": map[string]any{
			"type":      "function_call",
			"status":    "completed",
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
