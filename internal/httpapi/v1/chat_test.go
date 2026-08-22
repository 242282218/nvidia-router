package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/provider"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/sse"
	"nvidia-router/internal/upstream/nvidia"
	"nvidia-router/internal/xkproxy"
)

func TestChatRejectsOversizedBody(t *testing.T) {
	handler := NewChat(nil, nil, nil)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(strings.Repeat("x", config.JSONBodyLimit+1)),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertChatError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestChatOpenCodeFreeEmptyResponseReturnsFault(t *testing.T) {
	model := modelcatalog.Model{
		ID: 1, PublicID: "free-model", UpstreamID: "free-model",
		Kind: modelcatalog.KindChat, Provider: modelcatalog.ProviderOpenCodeFree, Enabled: true,
	}
	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return model, nil
	})

	for name, upstreamResponse := range map[string]*http.Response{
		"nil response": nil,
		"nil body":     {StatusCode: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			handler := NewChat(resolver, nil, nil).WithOpenCodeFree(openCodeFreeFunc(func(context.Context, runtimeconfig.Snapshot, []byte, bool) (*http.Response, error) {
				return upstreamResponse, nil
			}))
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"free-model","messages":[{"role":"user","content":"hi"}]}`))

			handler.ServeHTTP(response, request)

			assertChatError(t, response, http.StatusTooManyRequests, "upstream_empty_response")
		})
	}
}

func TestStreamWriteDeadlineUsesEarlierContextDeadline(t *testing.T) {
	settings := &deadlineBudgetSettings{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 100, FirstByteTimeoutMS: 100, StreamFirstTokenTimeoutMS: 100,
		StreamIdleTimeoutMS: 1000, RetryBudgetMS: 1000,
	}}
	keys := pool.New(settings, clock.RealClock{})
	keys.LoadSnapshot([]keystate.KeySnapshot{{ID: 1, Enabled: true}}, nil)
	keys.SetModelEnabled(10, true)
	attempt := router.NewAttempt(settings, keys, deadlineBudgetSecrets{}, deadlineBudgetStates{}, keys, clock.RealClock{})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(50*time.Millisecond))
	defer cancel()
	result, err := attempt.Run(ctx, 10, true, func(context.Context, int64, []byte, *router.CommitState) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer result.Release()
	if got := streamWriteDeadline(result.Context); got >= 200*time.Millisecond {
		t.Fatalf("write deadline = %s, want earlier context deadline", got)
	}
}

func TestSpeechChatResponsesWriteDeadlineExpiredContextReturnsImmediately(t *testing.T) {
	settings := &deadlineBudgetSettings{snapshot: runtimeconfig.Snapshot{
		ConnectTimeoutMS: 100, FirstByteTimeoutMS: 100, StreamFirstTokenTimeoutMS: 100,
		StreamIdleTimeoutMS: 1000, RetryBudgetMS: 1000,
	}}
	keys := pool.New(settings, clock.RealClock{})
	keys.LoadSnapshot([]keystate.KeySnapshot{{ID: 1, Enabled: true}}, nil)
	keys.SetModelEnabled(10, true)
	attempt := router.NewAttempt(settings, keys, deadlineBudgetSecrets{}, deadlineBudgetStates{}, keys, clock.RealClock{})
	result, err := attempt.Run(context.Background(), 10, true, func(context.Context, int64, []byte, *router.CommitState) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer result.Release()
	expired, cancel := context.WithDeadline(result.Context, time.Now().Add(-time.Second))
	defer cancel()
	if got := streamWriteDeadline(expired); got <= 0 || got > time.Millisecond {
		t.Fatalf("expired write deadline = %s, want immediate positive deadline", got)
	}
}

func TestChatNonstreamExecutionMarksSemanticCompletion(t *testing.T) {
	body := &semanticMarkBody{ReadCloser: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))}
	client := &semanticMarkProvider{response: &http.Response{StatusCode: http.StatusOK, Body: body}}
	response, err := NewChat(nil, nil, client).execute([]byte(`{"model":"vendor/model"}`), false)(
		context.Background(), 1, nil, &router.CommitState{},
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !body.completed {
		t.Fatal("valid non-stream response did not mark semantic completion")
	}
	_ = response.Body.Close()
}

func TestChatStreamEmptyDeltaBeforeDoneReturnsRetryableFault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	response, err := NewChat(nil, nil, client).execute([]byte(`{}`), true)(
		context.Background(), 1, []byte("upstream-secret"), &router.CommitState{},
	)
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	var classified fault.Fault
	if !errors.As(err, &classified) {
		t.Fatalf("execute error = %T %v, want fault.Fault", err, err)
	}
	if classified.HTTPStatus != http.StatusTooManyRequests || !classified.Retryable || classified.RetryAfter <= 0 {
		t.Fatalf("classified = %#v, want retryable 429 with Retry-After", classified)
	}
}

type deadlineBudgetSettings struct{ snapshot runtimeconfig.Snapshot }

func (s *deadlineBudgetSettings) Snapshot() runtimeconfig.Snapshot { return s.snapshot }

type deadlineBudgetSecrets struct{}

func (deadlineBudgetSecrets) WithSecret(_ context.Context, _ int64, callback func([]byte) error) error {
	return callback(nil)
}

type deadlineBudgetStates struct{}

func (deadlineBudgetStates) MarkSuccess(context.Context, int64) (keystate.KeySnapshot, error) {
	return keystate.KeySnapshot{}, nil
}
func (deadlineBudgetStates) MarkFailure(context.Context, int64, int64, fault.Fault) (keystate.KeySnapshot, error) {
	return keystate.KeySnapshot{}, nil
}

func TestChatRejectsBodyWhenReadSlotSaturated(t *testing.T) {
	// Saturate the body-read semaphore so the handler must refuse instead of
	// buffering another up-to-32MiB body ahead of pool admission.
	acquired := 0
	for {
		select {
		case bodyReadSemaphore <- struct{}{}:
			acquired++
		default:
			goto saturated
		}
	}
saturated:
	t.Cleanup(func() {
		for ; acquired > 0; acquired-- {
			<-bodyReadSemaphore
		}
	})

	handler := NewChat(nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(false)))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertChatError(t, response, http.StatusTooManyRequests, "server_busy")
}

func TestChatReturnsModelNotFound(t *testing.T) {
	handler := NewChat(modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelNotFound
	}), nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(false)))

	handler.ServeHTTP(response, request)

	assertChatError(t, response, http.StatusNotFound, "model_not_found")
}

func TestChatPassesParsedCapabilityRequirementsToModelResolver(t *testing.T) {
	var resolved modelcatalog.Requirements
	resolver := modelResolverFunc(func(_ context.Context, _ string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
		resolved = requirements
		return modelcatalog.Model{
			ID: 3, PublicID: "public-model", UpstreamID: "vendor/model", Kind: modelcatalog.KindChat,
			Enabled: true, SupportsTools: true, SupportsReasoning: true, ReasoningWireFormat: "openai",
		}, nil
	})
	called := false
	runner := attemptRunnerFunc(func(context.Context, int64, bool, router.ExecuteFunc) (router.AttemptResult, error) {
		called = true
		return router.AttemptResult{Response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}}, nil
	})
	handler := NewChat(resolver, runner, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public-model","messages":[{"role":"user","content":"use lookup"}],"tools":[{"type":"function","function":{"name":"lookup"}}],"reasoning_effort":"low"}`))

	handler.ServeHTTP(response, request)

	want := modelcatalog.Requirements{Kind: modelcatalog.KindChat, Tools: true, Reasoning: true}
	if resolved != want {
		t.Fatalf("resolved requirements = %+v, want %+v", resolved, want)
	}
	if !called || response.Code != http.StatusOK {
		t.Fatalf("provider path called=%v status=%d body=%s", called, response.Code, response.Body.String())
	}
}

func TestWriteChatErrorMapsProxyFailureToBadGateway(t *testing.T) {
	response := httptest.NewRecorder()

	writeChatError(response, xkproxy.NewTransportError(errors.New("private proxy cause")))

	assertChatError(t, response, http.StatusBadGateway, "upstream_proxy_unavailable")
}

func TestChatStreamForwardsSSEEvents(t *testing.T) {
	sseBody := "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(sseBody))
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 5}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("stream-key"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 3, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})
	handler := NewChat(resolver, runner, client)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(true)))

	handler.ServeHTTP(response, request)

	if !lease.released {
		t.Fatal("stream attempt lease was not released")
	}
	body := response.Body.String()
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("SSE [DONE] not found in response: %s", body)
	}
	ct := response.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if got := response.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want %q (audit B6: nginx must not buffer SSE)", got, "no")
	}
}

func TestSemanticChatEventAcceptsReasoningAliases(t *testing.T) {
	for _, data := range []string{
		`{"choices":[{"delta":{"reasoning":"step"}}]}`,
		`{"choices":[{"delta":{"thinking":"step"}}]}`,
	} {
		accepted, err := semanticChatEvent(sse.Event{Data: []string{data}})
		if err != nil || !accepted {
			t.Fatalf("semanticChatEvent(%s) = %v/%v, want true/nil", data, accepted, err)
		}
	}
}

func TestChatStreamCommentOnlyAttemptFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, ": keep-alive\n\n")
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	response, err := NewChat(nil, nil, client).execute([]byte(`{}`), true)(
		context.Background(), 1, []byte("upstream-secret"), &router.CommitState{},
	)
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	// A 200 with no data events is an upstream protocol defect: it must be
	// classified Protocol (not a retryable connection error) so the attempt
	// loop does not cool down healthy keys.
	var classified fault.Fault
	if !errors.As(err, &classified) {
		t.Fatalf("execute error = %T %v, want fault.Fault", err, err)
	}
	if classified.PublicCode != "upstream_protocol_error" || classified.Scope != fault.ScopeUpstreamGlobal {
		t.Fatalf("classified = %#v, want upstream_protocol_error global", classified)
	}
}

// TestChatStreamResponseUncommittedInterruptionWritesError locks the handler
// contract for an interrupted upstream stream that never delivered a data
// event: primeSSE normally intercepts this at execute time, but if it ever
// slips through (e.g. a future change to the prime path), the client must get
// a 502 protocol error, not an empty 200 it would mistake for a completion.
func TestChatStreamResponseUncommittedInterruptionWritesError(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(": keep-alive\n\n")),
	}
	response := httptest.NewRecorder()
	NewChat(nil, nil, nil).streamResponse(context.Background(), response, upstream)
	assertChatError(t, response, http.StatusBadGateway, "upstream_protocol_error")
}

// TestChatStreamCommittedInterruptionAppendsErrorEvent locks the contract for
// a stream that delivered events and then hit EOF without [DONE]: the client
// must receive an in-stream error event (plus a terminal [DONE]) so agents
// retry instead of treating the partial output as a complete answer. Before
// this the handler closed silently, and an upstream blip surfaced as an
// empty-but-successful completion.
func TestChatStreamCommittedInterruptionAppendsErrorEvent(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")),
	}
	logger := &recordingLogHandler{}
	ctx := observability.WithRequestLogger(context.Background(), slog.New(logger))
	response := httptest.NewRecorder()
	NewChat(nil, nil, nil).streamResponse(ctx, response, upstream)
	body := response.Body.String()
	if !strings.Contains(body, "upstream_stream_truncated") {
		t.Fatalf("body = %q, want in-stream truncation error event", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("body = %q, want terminal [DONE] after the error event", body)
	}
	if !logger.contains("stream_truncated_after_commit") {
		t.Fatalf("committed interruption was not logged; got %v", logger.messages())
	}
}

// TestChatStreamTruncationAfterCommitLogsWarn locks in that a stream which
// committed its first event and then died on an upstream error is logged at
// Warn. Before this, the error was swallowed: the client observed a truncated
// generation and ops had no trace that the upstream stalled.
func TestChatStreamTruncationAfterCommitLogsWarn(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &scriptedBody{
			chunks: [][]byte{[]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")},
			err:    errors.New("boom"),
		},
	}
	logger := &recordingLogHandler{}
	ctx := observability.WithRequestLogger(context.Background(), slog.New(logger))
	response := httptest.NewRecorder()
	NewChat(nil, nil, nil).streamResponse(ctx, response, upstream)
	if !logger.contains("stream_truncated_after_commit") {
		t.Fatalf("post-commit truncation was not logged; got %v", logger.messages())
	}
}

// TestChatStreamClientDisconnectAfterCommitLogsDebug keeps a client disconnect
// (context cancellation) after commit at Debug, so the Warn line above stays a
// reliable upstream-fault signal instead of drowning in expected churn.
func TestChatStreamClientDisconnectAfterCommitLogsDebug(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &scriptedBody{
			chunks: [][]byte{[]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")},
			err:    errors.New("boom"),
		},
	}
	logger := &recordingLogHandler{}
	ctx, cancel := context.WithCancel(observability.WithRequestLogger(context.Background(), slog.New(logger)))
	response := httptest.NewRecorder()
	// The context is already cancelled before the proxy runs: the first event
	// still commits (the scripted body ignores Close), then the read error is
	// attributed to the disconnect, not the upstream.
	cancel()
	NewChat(nil, nil, nil).streamResponse(ctx, response, upstream)
	if logger.contains("stream_truncated_after_commit") {
		t.Fatalf("client disconnect was logged as an upstream truncation: %v", logger.messages())
	}
	if !logger.contains("stream_context_cancelled_after_commit") {
		t.Fatalf("client disconnect after commit was not logged at Debug; got %v", logger.messages())
	}
}

// scriptedBody serves fixed chunks and then a fixed read error, letting tests
// exercise a stream that commits its first event and then dies.
type scriptedBody struct {
	chunks [][]byte
	index  int
	err    error
}

func (b *scriptedBody) Read(payload []byte) (int, error) {
	if b.index < len(b.chunks) {
		chunk := b.chunks[b.index]
		b.index++
		return copy(payload, chunk), nil
	}
	return 0, b.err
}

func (*scriptedBody) Close() error { return nil }

// recordingLogHandler captures slog messages so tests can assert on the log
// lines a handler emitted.
type recordingLogHandler struct {
	mu       sync.Mutex
	recorded []string
}

func (h *recordingLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recorded = append(h.recorded, record.Message)
	return nil
}

func (h *recordingLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingLogHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingLogHandler) contains(message string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.recorded {
		if m == message {
			return true
		}
	}
	return false
}

func (h *recordingLogHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.recorded...)
}

func TestChatRetriesProtocolFailureWithBackupKeyAndPreservesResponse(t *testing.T) {
	type capturedRequest struct {
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 2)
	responseBody := []byte(`{"id":"chat-1","choices":[{"message":{"role":"assistant","content":"ok"}}],"future":{"kept":true}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured <- capturedRequest{header: request.Header.Clone(), body: body}
		writer.Header().Set("Content-Type", "application/json")
		if request.Header.Get("Authorization") == "Bearer first-secret" {
			_, _ = writer.Write([]byte(`{"id":"chat-1","choices":[{"message":{"content":{"unexpected":true}}}]}`))
			return
		}
		_, _ = writer.Write(responseBody)
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 7}
	attempts := 0
	protocolFailures := 0
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		var lastErr error
		for index, secret := range []string{"first-secret", "second-secret"} {
			attempts++
			response, err := execute(ctx, int64(index+1), []byte(secret), &router.CommitState{})
			if err == nil {
				return router.AttemptResult{Response: response, Lease: lease, Attempts: attempts}, nil
			}
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			var classified fault.Fault
			if !errors.As(err, &classified) || !classified.Retryable || classified.PublicCode != "upstream_protocol_error" {
				return router.AttemptResult{}, err
			}
			protocolFailures++
			lastErr = err
		}
		return router.AttemptResult{}, lastErr
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
		if publicID != "public-model" || requirements.Kind != modelcatalog.KindChat {
			t.Fatalf("resolve = %q, %#v", publicID, requirements)
		}
		return modelcatalog.Model{
			ID: 11, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true,
		}, nil
	})
	handler := NewChat(resolver, runner, client)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(false)))

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), responseBody) {
		t.Fatalf("response = %d %s", response.Code, response.Body.Bytes())
	}
	// Audit B6: only streaming responses opt out of reverse-proxy buffering.
	// Non-streaming responses should let nginx buffer/compress as usual so the
	// SSE-specific X-Accel-Buffering header is never set on a 200 JSON reply.
	if got := response.Header().Get("X-Accel-Buffering"); got != "" {
		t.Fatalf("non-streaming response X-Accel-Buffering = %q, want unset", got)
	}
	if attempts != 2 || protocolFailures != 1 {
		t.Fatalf("attempts = %d, protocol failures = %d; want 2, 1", attempts, protocolFailures)
	}
	if !lease.released {
		t.Fatal("successful attempt lease was not released")
	}
	for _, wantAuthorization := range []string{"Bearer first-secret", "Bearer second-secret"} {
		got := <-captured
		if got.header.Get("Authorization") != wantAuthorization {
			t.Fatalf("Authorization = %q, want %q", got.header.Get("Authorization"), wantAuthorization)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(got.body, &fields); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if string(fields["model"]) != `"vendor/model"` || string(fields["future"]) != `{"kept":true}` {
			t.Fatalf("upstream fields = %s", got.body)
		}
	}
}

func TestChatExecutionPreservesResponseReadFailureClassification(t *testing.T) {
	readErr := io.ErrUnexpectedEOF
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Errorf("response writer does not support hijacking")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack upstream connection: %v", err)
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 20\r\n\r\n{\"choices\"")
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := NewChat(nil, nil, client).execute([]byte(`{}`), false)(
		context.Background(), 1, []byte("upstream-secret"), &router.CommitState{},
	)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("execute error = %v, want wrapped read error", err)
	}
	var classified fault.Fault
	if errors.As(err, &classified) && classified.PublicCode == "upstream_protocol_error" {
		t.Fatalf("read failure was misclassified as protocol error: %+v", classified)
	}
}

func validChatRequest(stream bool) string {
	return `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":` +
		map[bool]string{false: "false", true: "true"}[stream] + `,"future":{"kept":true}}`
}

// TestChatStreamRecordsFirstTokenThroughMiddleware locks the TTFT chain for the
// streaming path: the first SSE data event must land in the recorded request
// metadata as first_token_ms, distinct from the first-byte metric.
func TestChatStreamRecordsFirstTokenThroughMiddleware(t *testing.T) {
	sseBody := "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(sseBody))
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 5}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("stream-key"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 3, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})
	var recorded observability.RequestRecord
	recorder := requestRecorderFunc(func(_ context.Context, record observability.RequestRecord) error {
		recorded = record
		return nil
	})
	handler := observability.HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), NewChat(resolver, runner, client))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(true)))

	handler.ServeHTTP(response, request)

	if !strings.Contains(response.Body.String(), "[DONE]") {
		t.Fatalf("SSE [DONE] not found in response: %s", response.Body.String())
	}
	if recorded.FirstTokenMS == nil {
		t.Fatal("first_token_ms not recorded for a stream that produced a token")
	}
	if *recorded.FirstTokenMS < 0 {
		t.Fatalf("first_token_ms = %d, want >= 0", *recorded.FirstTokenMS)
	}
	if !recorded.IsStream {
		t.Fatalf("recorded IsStream = false, want true")
	}
}

type requestRecorderFunc func(context.Context, observability.RequestRecord) error

func (f requestRecorderFunc) Record(ctx context.Context, record observability.RequestRecord) error {
	return f(ctx, record)
}

// TestChatNonstreamRecordsReasoningRequestAndResponse locks the non-stream
// reasoning observability chain: the upstream body's reasoning_effort must be
// recorded as reasoning_requested plus wire field names, and the response's
// reasoning_content length must land in reasoning_chars.
func TestChatNonstreamRecordsReasoningRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"reasoning_content":"deep thought","content":"answer"}}]}`))
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, 1, []byte("stream-key"), &router.CommitState{})
		return router.AttemptResult{Response: response, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{
			ID: 3, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true,
			SupportsReasoning: true, ReasoningWireFormat: "openai", ReasoningLevels: []string{"low", "medium"},
			ReasoningMaxBudget: 8192, ReasoningDynamicAllowed: false,
		}, nil
	})
	var recorded observability.RequestRecord
	recorder := requestRecorderFunc(func(_ context.Context, record observability.RequestRecord) error {
		recorded = record
		return nil
	})
	handler := observability.HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), NewChat(resolver, runner, client))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`,
	))

	handler.ServeHTTP(response, request)

	if !recorded.ReasoningRequested {
		t.Fatal("reasoning_requested = false, want true")
	}
	if recorded.ReasoningWireFields != "reasoning_effort" {
		t.Fatalf("reasoning_wire_fields = %q, want reasoning_effort", recorded.ReasoningWireFields)
	}
	if recorded.ReasoningRequestedLevel != "high" {
		t.Fatalf("reasoning_requested_level = %q, want high", recorded.ReasoningRequestedLevel)
	}
	if recorded.ReasoningEffectiveLevel != "medium" {
		t.Fatalf("reasoning_effective_level = %q, want medium", recorded.ReasoningEffectiveLevel)
	}
	if !recorded.ReasoningPresent {
		t.Fatal("reasoning_present = false, want true")
	}
	if recorded.ReasoningChars == nil || *recorded.ReasoningChars != 12 {
		t.Fatalf("reasoning_chars = %#v, want 12", recorded.ReasoningChars)
	}
	if recorded.RouteMode != "direct" {
		t.Fatalf("route_mode = %q, want direct", recorded.RouteMode)
	}
}

// TestChatStreamRecordsStreamDoneAndReasoningChars locks the streaming path:
// reasoning_content deltas must accumulate into reasoning_chars and a [DONE]
// marker must set stream_done.
func TestChatStreamRecordsStreamDoneAndReasoningChars(t *testing.T) {
	sseBody := "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"reasoning_content\":\"chain\"}}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(sseBody))
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, 1, []byte("stream-key"), &router.CommitState{})
		return router.AttemptResult{Response: response, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 3, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})
	var recorded observability.RequestRecord
	recorder := requestRecorderFunc(func(_ context.Context, record observability.RequestRecord) error {
		recorded = record
		return nil
	})
	handler := observability.HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), NewChat(resolver, runner, client))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high","stream":true}`,
	))

	handler.ServeHTTP(response, request)

	if !recorded.ReasoningRequested {
		t.Fatal("reasoning_requested = false, want true")
	}
	if !recorded.ReasoningPresent {
		t.Fatal("reasoning_present = false, want true")
	}
	if recorded.ReasoningChars == nil || *recorded.ReasoningChars != 5 {
		t.Fatalf("reasoning_chars = %#v, want 5", recorded.ReasoningChars)
	}
	if !recorded.StreamDone {
		t.Fatal("stream_done = false, want true after [DONE]")
	}
}

// TestChatStreamWithoutReasoningStaysFalse ensures plain streams do not pay
// reasoning sampling cost nor report reasoning fields, while stream_done is
// still recorded.
func TestChatStreamWithoutReasoningStaysFalse(t *testing.T) {
	sseBody := "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(sseBody))
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, 1, []byte("stream-key"), &router.CommitState{})
		return router.AttemptResult{Response: response, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 3, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})
	var recorded observability.RequestRecord
	recorder := requestRecorderFunc(func(_ context.Context, record observability.RequestRecord) error {
		recorded = record
		return nil
	})
	handler := observability.HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), NewChat(resolver, runner, client))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(true)))

	handler.ServeHTTP(response, request)

	if recorded.ReasoningRequested {
		t.Fatal("reasoning_requested = true, want false")
	}
	if recorded.ReasoningPresent {
		t.Fatal("reasoning_present = true, want false")
	}
	if recorded.ReasoningChars != nil {
		t.Fatalf("reasoning_chars = %#v, want nil", recorded.ReasoningChars)
	}
	if !recorded.StreamDone {
		t.Fatal("stream_done = false, want true after [DONE]")
	}
}

func assertChatError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if payload.Error.Code != code {
		t.Fatalf("code = %q, want %q", payload.Error.Code, code)
	}
}

type modelResolverFunc func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error)

func (f modelResolverFunc) Resolve(ctx context.Context, id string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
	return f(ctx, id, requirements)
}

type openCodeFreeFunc func(context.Context, runtimeconfig.Snapshot, []byte, bool) (*http.Response, error)

func (f openCodeFreeFunc) Chat(ctx context.Context, snapshot runtimeconfig.Snapshot, body []byte, stream bool) (*http.Response, error) {
	return f(ctx, snapshot, body, stream)
}

type attemptRunnerFunc func(context.Context, int64, bool, router.ExecuteFunc) (router.AttemptResult, error)

func (f attemptRunnerFunc) Run(ctx context.Context, modelID int64, stream bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
	return f(ctx, modelID, stream, execute)
}

type releaseTrackingLease struct {
	id       int64
	released bool
}

func (l *releaseTrackingLease) KeyID() int64 { return l.id }

func (l *releaseTrackingLease) Release() { l.released = true }

type semanticMarkBody struct {
	io.ReadCloser
	completed bool
}

func (b *semanticMarkBody) MarkComplete() { b.completed = true }

type semanticMarkProvider struct {
	response *http.Response
}

func (p *semanticMarkProvider) ID() string { return "test" }

func (p *semanticMarkProvider) Models(context.Context, string) ([]string, error) {
	return nil, errors.New("not used")
}

func (p *semanticMarkProvider) Chat(context.Context, runtimeconfig.Snapshot, string, []byte, bool) (*http.Response, error) {
	return p.response, nil
}

func (p *semanticMarkProvider) Embeddings(context.Context, runtimeconfig.Snapshot, string, []byte) (*http.Response, error) {
	return nil, errors.New("not used")
}

func (p *semanticMarkProvider) AudioTranscriptionsReplay(context.Context, runtimeconfig.Snapshot, string, router.ReplayableBody, string) (*http.Response, error) {
	return nil, errors.New("not used")
}

func (p *semanticMarkProvider) AudioSpeech(context.Context, runtimeconfig.Snapshot, string, []byte) (*http.Response, error) {
	return nil, errors.New("not used")
}

func (p *semanticMarkProvider) CapabilityHint(string) provider.CapabilityHint {
	return provider.CapabilityHint{}
}
