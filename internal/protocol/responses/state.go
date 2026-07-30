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
// indices and flags only; reasoning text, completion text and tool arguments
// are forwarded as deltas and discarded.
type streamState struct {
	sequence       int
	itemIndex      int
	messageStarted bool
	textPartOpen   bool
	openTools      map[int]*toolItem
	toolOrder      []int
	finished       bool
}

type toolItem struct {
	outputIndex int
	id          string
	name        string
	arguments   stringsBuilder
	closed      bool
}

func newStreamState() *streamState {
	return &streamState{openTools: make(map[int]*toolItem)}
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

// stringsBuilder is a minimal accumulating helper kept in-package so the state
// machine never imports strings via std Builder helpers that grow unbounded;
// callers bound total size at the HTTP layer.
type stringsBuilder struct {
	parts []string
}

func (b *stringsBuilder) write(fragment string) { b.parts = append(b.parts, fragment) }

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
