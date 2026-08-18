package observability

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type requestStateKey struct{}

type RequestState struct {
	mu                sync.Mutex
	ModelID           string
	AccessKeyID       *int64
	NVIDIAKeyID       *int64
	ErrorCode         *string
	IsStream          bool
	QueueMS           int64
	AttemptCount      int
	PromptTokens      *int64
	CompletionTokens  *int64
	UpstreamRequestID *string
	// FirstTokenAt is set by the streaming handler the instant the first SSE
	// data event reaches the client. The middleware converts it to a TTFT in
	// milliseconds, mirroring first_byte_ms.
	FirstTokenAt       time.Time
	firstTokenRecorded bool
	// Reasoning observability carries only booleans, field names and character
	// counts; reasoning text is never retained.
	ReasoningRequested      bool
	ReasoningWireFields     string
	ReasoningRequestedLevel string
	ReasoningEffectiveLevel string
	ReasoningPresent        bool
	ReasoningChars          *int64
	StreamDone              bool
	RouteMode               string
	UsageRecorder           func(*int64, *int64)
}

func WithRequestState(ctx context.Context) (context.Context, *RequestState) {
	state := &RequestState{}
	return context.WithValue(ctx, requestStateKey{}, state), state
}

type requestLoggerKey struct{}

// WithRequestLogger stores the request-scoped logger on the context so handlers
// deep in the chain can log request-scoped events without threading a logger
// through every constructor. The middleware injects it once per request.
func WithRequestLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, requestLoggerKey{}, logger)
}

// RequestLogger returns the logger stored by WithRequestLogger, or the default
// logger when the middleware did not wrap the request (direct handler tests).
func RequestLogger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(requestLoggerKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}

func SetAccessKey(ctx context.Context, id int64) {
	updateState(ctx, func(state *RequestState) { state.AccessKeyID = int64Pointer(id) })
}

func SetModel(ctx context.Context, modelID string, stream bool) {
	updateState(ctx, func(state *RequestState) {
		state.ModelID = modelID
		state.IsStream = stream
	})
}

func SetAttempt(ctx context.Context, keyID int64, attempts int, queue time.Duration) {
	updateState(ctx, func(state *RequestState) {
		state.NVIDIAKeyID = int64Pointer(keyID)
		state.AttemptCount = attempts
		state.QueueMS = queue.Milliseconds()
	})
}

func SetUpstreamRequestID(ctx context.Context, value string) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return
	}
	updateState(ctx, func(state *RequestState) { state.UpstreamRequestID = stringPointer(value) })
}

// SetFirstTokenAt records the instant the first SSE data event reached the
// client. Only the first call wins so a replayed or re-entered stream never
// overwrites the original TTFT. A zero time is ignored.
func SetFirstTokenAt(ctx context.Context, at time.Time) {
	if at.IsZero() {
		return
	}
	updateState(ctx, func(state *RequestState) {
		if state.firstTokenRecorded {
			return
		}
		state.FirstTokenAt = at
		state.firstTokenRecorded = true
	})
}

func SetErrorCode(ctx context.Context, code string) {
	if code == "" {
		return
	}
	updateState(ctx, func(state *RequestState) { state.ErrorCode = stringPointer(code) })
}

// SetReasoningRequest records whether the upstream body requested reasoning and
// which field names carried that request. Only field names, never their values,
// are stored.
func SetReasoningRequest(ctx context.Context, requested bool, wireFields string) {
	updateState(ctx, func(state *RequestState) {
		state.ReasoningRequested = requested
		state.ReasoningWireFields = wireFields
	})
}

func SetReasoningLevels(ctx context.Context, requested, effective string) {
	requested = safeReasoningLevel(requested)
	effective = safeReasoningLevel(effective)
	updateState(ctx, func(state *RequestState) {
		state.ReasoningRequestedLevel = requested
		state.ReasoningEffectiveLevel = effective
	})
}

// SetReasoningResponse records whether the upstream response carried reasoning
// and its character count. Reasoning text is never retained; chars is only set
// when reasoning is present.
func SetReasoningResponse(ctx context.Context, present bool, chars int64) {
	updateState(ctx, func(state *RequestState) {
		state.ReasoningPresent = present
		if present {
			state.ReasoningChars = int64Pointer(chars)
		} else {
			state.ReasoningChars = nil
		}
	})
}

func SetStreamDone(ctx context.Context, done bool) {
	updateState(ctx, func(state *RequestState) { state.StreamDone = done })
}

func SetRouteMode(ctx context.Context, mode string) {
	updateState(ctx, func(state *RequestState) { state.RouteMode = mode })
}

// ReasoningRequested reports whether the current request asked for reasoning. It
// is a snapshot read so streaming handlers can gate per-event parsing once.
func ReasoningRequested(ctx context.Context) bool {
	state, ok := ctx.Value(requestStateKey{}).(*RequestState)
	if !ok {
		return false
	}
	snapshot := state.Snapshot()
	return snapshot.ReasoningRequested
}

func SetUsage(ctx context.Context, prompt, completion *int64) {
	updateState(ctx, func(state *RequestState) {
		state.PromptTokens = prompt
		state.CompletionTokens = completion
	})
}

func RecordUsage(ctx context.Context, prompt, completion *int64) {
	state, ok := ctx.Value(requestStateKey{}).(*RequestState)
	if !ok {
		return
	}
	var recorder func(*int64, *int64)
	state.mu.Lock()
	recorder = state.UsageRecorder
	state.mu.Unlock()
	if recorder != nil {
		recorder(prompt, completion)
	}
}

func SetUsageRecorder(ctx context.Context, recorder func(*int64, *int64)) {
	updateState(ctx, func(state *RequestState) { state.UsageRecorder = recorder })
}

func UsageFromContext(ctx context.Context) (*int64, *int64) {
	state, ok := ctx.Value(requestStateKey{}).(*RequestState)
	if !ok {
		return nil, nil
	}
	snapshot := state.Snapshot()
	return snapshot.PromptTokens, snapshot.CompletionTokens
}

func (s *RequestState) Snapshot() RequestState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RequestState{
		ModelID: s.ModelID, AccessKeyID: s.AccessKeyID, NVIDIAKeyID: s.NVIDIAKeyID,
		ErrorCode: s.ErrorCode, IsStream: s.IsStream, QueueMS: s.QueueMS,
		AttemptCount: s.AttemptCount, PromptTokens: s.PromptTokens,
		CompletionTokens: s.CompletionTokens, UpstreamRequestID: s.UpstreamRequestID,
		FirstTokenAt:       s.FirstTokenAt,
		ReasoningRequested: s.ReasoningRequested, ReasoningWireFields: s.ReasoningWireFields,
		ReasoningRequestedLevel: s.ReasoningRequestedLevel, ReasoningEffectiveLevel: s.ReasoningEffectiveLevel,
		ReasoningPresent: s.ReasoningPresent, ReasoningChars: s.ReasoningChars,
		StreamDone: s.StreamDone, RouteMode: s.RouteMode,
	}
}

func safeReasoningLevel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func updateState(ctx context.Context, update func(*RequestState)) {
	state, ok := ctx.Value(requestStateKey{}).(*RequestState)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	update(state)
}

func int64Pointer(value int64) *int64    { return &value }
func stringPointer(value string) *string { return &value }
