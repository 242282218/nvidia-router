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

func (s *RequestState) Snapshot() RequestState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RequestState{
		ModelID: s.ModelID, AccessKeyID: s.AccessKeyID, NVIDIAKeyID: s.NVIDIAKeyID,
		ErrorCode: s.ErrorCode, IsStream: s.IsStream, QueueMS: s.QueueMS,
		AttemptCount: s.AttemptCount, PromptTokens: s.PromptTokens,
		CompletionTokens: s.CompletionTokens, UpstreamRequestID: s.UpstreamRequestID,
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
