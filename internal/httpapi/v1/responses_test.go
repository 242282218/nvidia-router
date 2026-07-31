package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
	"nvidia-router/internal/upstream/nvidia"
)

func TestResponsesNonStreamText(t *testing.T) {
	upstreamChat := []byte(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(upstreamChat)
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 5}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, req modelcatalog.Requirements) (modelcatalog.Model, error) {
		if publicID != "public-chat" || req.Kind != modelcatalog.KindChat {
			t.Fatalf("resolve = %q, %#v", publicID, req)
		}
		return modelcatalog.Model{ID: 3, PublicID: "public-chat", UpstreamID: "vendor/chat", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})
	handler := NewResponses(resolver, runner, client)
	body := `{"model":"public-chat","input":"hi"}`
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !lease.released {
		t.Fatal("attempt lease was not released")
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode responses body: %v; body=%s", err, response.Body.String())
	}
	if !strings.HasPrefix(result["id"].(string), "resp_") {
		t.Fatalf("id = %v, want resp_ prefix", result["id"])
	}
	if result["object"] != "response" {
		t.Fatalf("object = %v", result["object"])
	}
	if result["status"] != "completed" {
		t.Fatalf("status = %v", result["status"])
	}
	if result["model"] != "public-chat" {
		t.Fatalf("model = %v, want public-chat", result["model"])
	}
	output := result["output"].([]any)
	first := output[0].(map[string]any)
	if first["type"] != "message" {
		t.Fatalf("output type = %v", first["type"])
	}
	content := first["content"].([]any)
	if content[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("text = %v", content[0].(map[string]any)["text"])
	}
	usage := result["usage"].(map[string]any)
	if usage["input_tokens"] != float64(3) || usage["output_tokens"] != float64(2) {
		t.Fatalf("usage = %v", usage)
	}
}

func TestResponsesReusesAttemptForResolvedModel(t *testing.T) {
	upstreamChat := []byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(upstreamChat)
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, _ := nvidia.NewClient(upstream.Client(), descriptor)

	var resolvedModelID int64 = 42
	lease := &releaseTrackingLease{id: 9}
	runner := attemptRunnerFunc(func(ctx context.Context, modelID int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		if modelID != resolvedModelID {
			t.Fatalf("attempt model id = %d, want %d", modelID, resolvedModelID)
		}
		response, err := execute(ctx, lease.id, []byte("secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, _ string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: resolvedModelID, PublicID: "public-chat", UpstreamID: "vendor/chat", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})

	response := httptest.NewRecorder()
	NewResponses(resolver, runner, client).ServeHTTP(response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"public-chat","input":"x"}`)))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !lease.released {
		t.Fatal("attempt lease was not released")
	}
}

func TestResponsesStreamEmitsResponsesEventSequence(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"He\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	response, lease := serveStreamResponses(t, sseBody)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !lease.released {
		t.Fatal("stream attempt lease was not released")
	}
	body := response.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.in_progress",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		"event: response.output_text.done",
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
		"data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in stream response:\n%s", want, body)
		}
	}
	if strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("[DONE] appeared %d times, want 1", strings.Count(body, "data: [DONE]"))
	}
	if strings.Contains(body, "vendor/secret-id") || strings.Contains(body, "upstream-secret") {
		t.Fatalf("upstream secret or id leaked into stream response:\n%s", body)
	}
	ct := response.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestResponsesStreamParallelToolCallsReleasesLease(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"fc_1\",\"function\":{\"name\":\"get\",\"arguments\":\"{\\\"a\\\":1}\"}},{\"index\":1,\"id\":\"fc_2\",\"function\":{\"name\":\"send\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	response, lease := serveStreamResponses(t, sseBody)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !lease.released {
		t.Fatal("stream attempt lease was not released")
	}
	if !strings.Contains(response.Body.String(), "event: response.function_call_arguments.done") {
		t.Fatalf("missing tool arguments.done:\n%s", response.Body.String())
	}
	if strings.Count(response.Body.String(), "event: response.output_item.added") != 2 {
		t.Fatalf("expected 2 tool output_item.added events")
	}
}

func TestResponsesStreamEmitsFailedTerminalOnInterruption(t *testing.T) {
	// Upstream sends one delta then closes with neither finish_reason nor [DONE].
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	response, lease := serveStreamResponses(t, sseBody)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !lease.released {
		t.Fatal("stream attempt lease was not released")
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: response.failed") {
		t.Fatalf("missing response.failed terminal on interruption:\n%s", body)
	}
	if strings.Contains(body, "event: response.completed") {
		t.Fatalf("response.completed must not appear on interruption:\n%s", body)
	}
	if strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("[DONE] appeared %d times, want 1", strings.Count(body, "data: [DONE]"))
	}
}

func TestResponsesStreamDoneCompletesWithoutFinishReason(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"}}]}\n\n" +
		"data: [DONE]\n\n"
	response, _ := serveStreamResponses(t, sseBody)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: response.completed") || strings.Contains(body, "event: response.failed") {
		t.Fatalf("unexpected terminal events:\n%s", body)
	}
	if strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("[DONE] appeared %d times, want 1", strings.Count(body, "data: [DONE]"))
	}
}

func TestResponsesStreamDuplicateDoneEmitsSingleTerminal(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"}}]}\n\n" +
		"data: [DONE]\n\n" +
		"data: [DONE]\n\n"
	response, _ := serveStreamResponses(t, sseBody)

	body := response.Body.String()
	if strings.Count(body, "event: response.completed") != 1 || strings.Count(body, "event: response.failed") != 0 {
		t.Fatalf("terminal event counts unexpected:\n%s", body)
	}
	if strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("[DONE] appeared %d times, want 1", strings.Count(body, "data: [DONE]"))
	}
}

func TestResponsesStreamMalformedAfterCommitEmitsFailedTerminal(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {not-json}\n\n"
	response, _ := serveStreamResponses(t, sseBody)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Count(body, "event: response.failed") != 1 || strings.Count(body, "event: response.completed") != 0 {
		t.Fatalf("terminal event counts unexpected:\n%s", body)
	}
	if strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("[DONE] appeared %d times, want 1", strings.Count(body, "data: [DONE]"))
	}
}

// serveStreamResponses wires a Responses handler against a scripted upstream// Chat SSE stream and returns the recorder alongside the tracked lease.
func serveStreamResponses(t *testing.T, sseBody string) (*httptest.ResponseRecorder, *releaseTrackingLease) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(sseBody))
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 5}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, _ string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 3, PublicID: "public-chat", UpstreamID: "vendor/secret-id", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})
	handler := NewResponses(resolver, runner, client)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"public-chat","input":"x","stream":true}`))
	handler.ServeHTTP(response, request)
	return response, lease
}

func TestResponsesRejectsMissingModel(t *testing.T) {
	handler := NewResponses(nil, nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"x"}`)))

	assertChatError(t, response, http.StatusBadRequest, "invalid_parameter")
}

func TestResponsesReturnsModelNotFound(t *testing.T) {
	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelNotFound
	})
	handler := NewResponses(resolver, nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ghost","input":"x"}`)))

	assertChatError(t, response, http.StatusNotFound, "model_not_found")
}

func TestResponsesRejectsInvalidStreamFlag(t *testing.T) {
	handler := NewResponses(nil, nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"public-chat","input":"x","stream":"yes"}`)))

	assertChatError(t, response, http.StatusBadRequest, "invalid_parameter")
}

func TestResponsesDoesNotLeakUpstreamModel(t *testing.T) {
	upstreamChat := []byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(upstreamChat)
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, _ := nvidia.NewClient(upstream.Client(), descriptor)
	lease := &releaseTrackingLease{id: 1}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, _ string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 1, PublicID: "public-chat", UpstreamID: "vendor/secret-id", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})

	response := httptest.NewRecorder()
	NewResponses(resolver, runner, client).ServeHTTP(response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"public-chat","input":"x"}`)))

	if strings.Contains(response.Body.String(), "vendor/secret-id") {
		t.Fatalf("upstream id leaked into response body: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "upstream-secret") {
		t.Fatalf("upstream secret leaked into response body: %s", response.Body.String())
	}
}

func TestResponsesForwardsUpstreamErrorStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":"rate limited"}`))
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, _ := nvidia.NewClient(upstream.Client(), descriptor)
	lease := &releaseTrackingLease{id: 1}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, _ string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 1, PublicID: "public-chat", UpstreamID: "vendor/chat", Kind: modelcatalog.KindChat, Enabled: true}, nil
	})

	response := httptest.NewRecorder()
	NewResponses(resolver, runner, client).ServeHTTP(response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"public-chat","input":"x"}`)))

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", response.Code, response.Body.String())
	}
}
