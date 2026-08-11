package responses

import "time"

// EmittedEvent is a single logical Responses SSE event produced by the stream
// state machine. The Data map is a placeholder for event-specific payload
// fields; the state machine never records prompt or completion text here beyond
// what is required to forward delta payloads to the client.
type EmittedEvent struct {
	Event string
	Data  map[string]any
}

// Payload renders the JSON body for the event. The OpenAI SDKs dispatch on the
// payload's "type" field rather than the SSE "event:" header, so a payload
// without it is undispatchable even though the header is correct. Injecting it
// here keeps every construction site from having to repeat the event name.
func (e EmittedEvent) Payload() map[string]any {
	payload := make(map[string]any, len(e.Data)+1)
	for key, value := range e.Data {
		payload[key] = value
	}
	payload["type"] = e.Event
	return payload
}

// Emitter receives state-machine events. Commit reports whether the caller
// should treat the upstream stream as committed to the client after this event;
// the first user-visible event commits the response so later upstream failures
// cannot trigger a key switch.
type Emitter interface {
	Emit(event EmittedEvent) error
	Commit() error
}

// ErrStreamCompleted is returned by the SSE source when the upstream emitted
// the terminal [DONE] marker. It is intentionally distinct from EOF so a
// syntactically complete stream can complete even without finish_reason.
var ErrStreamCompleted = sentinel("upstream stream completed with [DONE]")

// ErrStreamInterrupted is returned by Stream when upstream ended before the
// terminal [DONE], after the state machine already produced a stable sequence.
// The HTTP layer maps it to repair-or-terminal decisions using the CommitState.
var ErrStreamInterrupted = sentinel("upstream stream interrupted before [DONE]")

// Stream drives the Responses stream state machine to completion or
// interruption. It is the public entry point used by HTTP handlers; the HTTP
// layer supplies the delta source and the emitter and decides how interruption
// is mapped once Stream returns.
func Stream(source ChatDeltaSource, emit Emitter, responseID, model string) (interrupted bool, err error) {
	return newStreamState().Convert(source, emit, responseID, model)
}

// streamState tracks per-request structural progress for a single Responses
// stream. It lives only for the request lifecycle and never persists. It holds
// indices and flags plus the accumulated assistant text and reasoning summary
// needed for the terminal done events; reasoning text, completion text and tool
// arguments are forwarded as deltas and only retained up to their budgets.
type streamState struct {
	sequence int
	// createdAt is captured once so every lifecycle event reports the same
	// response creation time, as clients use it to order responses.
	createdAt    int64
	itemIndex    int
	messageIndex int
	// messageID correlates every event for the assistant message item. It is
	// synthesized from the response id because chat deltas carry no message id.
	messageID      string
	messageStarted bool
	textPartOpen   bool
	// text accumulates the assistant content so output_text.done and the
	// message output_item.done carry the final text instead of an empty value
	// (SDKs assemble output from the done events).
	text *stringsBuilder
	// reasoningIndex/reasoningID track the open reasoning item; reasoning
	// summaries get their own output item before the message item.
	reasoningIndex int
	reasoningID    string
	reasoningOpen  bool
	reasoning      *stringsBuilder
	openTools      map[int]*toolItem
	toolOrder      []int
	// finishReason is the delta-level finish_reason from the last chunk. It is
	// consumed by finalize to report length/content_filter truncation as an
	// incomplete response instead of a silent completed one.
	finishReason string
	usage        *ChatUsage
	finalized    bool
}

type toolItem struct {
	outputIndex int
	id          string
	name        string
	arguments   *stringsBuilder
	closed      bool
}

func newStreamState() *streamState {
	return &streamState{
		createdAt: time.Now().Unix(),
		openTools: make(map[int]*toolItem),
		text:      newBoundedBuilder(maxStreamTextBytes, ErrStreamTextTooLarge),
		reasoning: newBoundedBuilder(maxStreamTextBytes, ErrStreamTextTooLarge),
	}
}

// nextSequence returns 0 for the first event, matching OpenAI's numbering.
func (s *streamState) nextSequence() int {
	current := s.sequence
	s.sequence++
	return current
}

// responseObject builds the nested "response" payload carried by the response
// lifecycle events. The SDKs read these fields from response.* rather than from
// the event root, so flattening them here would leave the response
// indistinguishable from an empty one.
func (s *streamState) responseObject(responseID, model, status string) map[string]any {
	response := map[string]any{
		"object":     "response",
		"created_at": s.createdAt,
		"status":     status,
	}
	if responseID != "" {
		response["id"] = responseID
	}
	if model != "" {
		response["model"] = model
	}
	return response
}

// event builds a response lifecycle event with the sequence number and the
// nested response object already populated.
func (s *streamState) event(name, responseID, model string) EmittedEvent {
	return EmittedEvent{Event: name, Data: map[string]any{
		"sequence_number": s.nextSequence(),
		"response":        s.responseObject(responseID, model, "in_progress"),
	}}
}

// outputItems assembles the final output array in output_index order. Slots are
// filled by index rather than sorted because every item reserved a unique dense
// index as it opened. It reads the id fields rather than the open flags, since
// finalize closes items before building the terminal event.
func (s *streamState) outputItems() []any {
	slots := make([]any, s.itemIndex)
	if s.reasoningID != "" && s.reasoningIndex < len(slots) {
		summary := []any{}
		if text := s.reasoning.string(); text != "" {
			summary = []any{map[string]any{"type": "summary_text", "text": text}}
		}
		slots[s.reasoningIndex] = map[string]any{
			"id": s.reasoningID, "type": "reasoning", "summary": summary,
		}
	}
	if s.messageStarted && s.messageIndex < len(slots) {
		content := []any{}
		if text := s.text.string(); text != "" {
			content = []any{map[string]any{"type": "output_text", "text": text}}
		}
		slots[s.messageIndex] = map[string]any{
			"id": s.messageID, "type": "message", "role": "assistant",
			"status": "completed", "content": content,
		}
	}
	for _, index := range s.toolOrder {
		tool := s.openTools[index]
		if tool == nil || tool.outputIndex >= len(slots) {
			continue
		}
		slots[tool.outputIndex] = map[string]any{
			"type": "function_call", "id": tool.id, "call_id": tool.id,
			"name": tool.name, "arguments": tool.arguments.string(),
		}
	}
	items := make([]any, 0, len(slots))
	for _, slot := range slots {
		if slot != nil {
			items = append(items, slot)
		}
	}
	return items
}

// maxToolArgumentsBytes bounds the total accumulated arguments of one tool call
// across a stream. The SSE decoder caps a single event at 4 MiB, but a hostile
// or broken upstream could stream argument fragments forever; without a total
// cap the tool item would grow without bound (checklist #14).
const maxToolArgumentsBytes = 16 << 20

// maxStreamTextBytes bounds the accumulated assistant text and reasoning
// summary kept for the terminal done events. Clients that assemble output from
// output_text.done / output_item.done need the final text, so the state machine
// must retain it; the cap mirrors the tool-argument budget.
const maxStreamTextBytes = 16 << 20

// ErrToolArgumentsTooLarge is returned when accumulated tool call arguments
// exceed maxToolArgumentsBytes. The HTTP layer treats it as an in-stream
// failure that terminates the response.
var ErrToolArgumentsTooLarge = sentinel("tool call arguments exceed maximum size")

// ErrStreamTextTooLarge is returned when accumulated assistant text or
// reasoning summary exceeds maxStreamTextBytes.
var ErrStreamTextTooLarge = sentinel("streamed response text exceeds maximum size")

// stringsBuilder is a minimal accumulating helper kept in-package so the state
// machine never imports strings via std Builder helpers that grow unbounded.
// The total byte count is enforced here so a stream of deltas cannot accumulate
// past the builder's budget.
type stringsBuilder struct {
	parts []string
	total int
	limit int
	err   error
}

func newBoundedBuilder(limit int, err error) *stringsBuilder {
	return &stringsBuilder{limit: limit, err: err}
}

func (b *stringsBuilder) write(fragment string) error {
	if b.total+len(fragment) > b.limit {
		return b.err
	}
	b.parts = append(b.parts, fragment)
	b.total += len(fragment)
	return nil
}

func (b *stringsBuilder) string() string {
	if len(b.parts) == 0 {
		return ""
	}
	total := 0
	for _, p := range b.parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range b.parts {
		out = append(out, p...)
	}
	return string(out)
}
