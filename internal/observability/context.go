package observability

import (
	"context"
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
	UsageRecorder      func(*int64, *int64)
}

func WithRequestState(ctx context.Context) (context.Context, *RequestState) {
	state := &RequestState{}
	return context.WithValue(ctx, requestStateKey{}, state), state
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
		FirstTokenAt: s.FirstTokenAt,
	}
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
