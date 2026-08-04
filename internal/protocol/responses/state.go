package responses

// EmittedEvent is a single logical Responses SSE event produced by the stream
// state machine. The Data map is a placeholder for event-specific payload
// fields; the state machine never records prompt or completion text here beyond
// what is required to forward delta payloads to the client.
type EmittedEvent struct {
	Event string
	Data  map[string]any
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
	sequence     int
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
	finished       bool
	usage          *ChatUsage
	finalized      bool
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
		openTools: make(map[int]*toolItem),
		text:      newBoundedBuilder(maxStreamTextBytes, ErrStreamTextTooLarge),
		reasoning: newBoundedBuilder(maxStreamTextBytes, ErrStreamTextTooLarge),
	}
}

func (s *streamState) nextSequence() int {
	s.sequence++
	return s.sequence
}

// emitted is a small helper to build an event with the response id and the
// next sequence number already populated.
func (s *streamState) event(name, responseID, model string) EmittedEvent {
	data := map[string]any{
		"sequence_number": s.nextSequence(),
	}
	if responseID != "" {
		data["id"] = responseID
	}
	if name == "response.created" || name == "response.completed" || name == "response.failed" || name == "response.in_progress" {
		data["object"] = "response"
		if model != "" {
			data["model"] = model
		}
	}
	return EmittedEvent{Event: name, Data: data}
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
