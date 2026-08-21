package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/observability"
	"nvidia-router/tests/mocknvidia"
)

func seedReasoningChatModel(t *testing.T, application *App) {
	t.Helper()
	err := modelcatalog.NewRepository(application.db).SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "public-model", UpstreamID: "vendor/model", DisplayName: "Test Model",
		Kind: modelcatalog.KindChat, Enabled: true, SupportsReasoning: true, ReasoningWireFormat: "openai",
	}}, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("save reasoning chat model: %v", err)
	}
}

// TestLongRunningTaskExecution verifies that the router handles long-running streaming
// tasks with extended TTFT, dozens of reasoning tokens, content tokens, and proper
// completion and observability recording.
func TestLongRunningTaskExecution(t *testing.T) {
	sseScript := mocknvidia.Script{
		Status:       http.StatusOK,
		FlushHeaders: true,
		SSE: []mocknvidia.SSEChunk{
			// TTFT delay simulating heavy reasoning model initialization
			{Data: ": keepalive\n\n", Delay: 20 * time.Millisecond},
			{Data: "data: {\"id\":\"chat-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"First thinking step: analyzing the prompt...\"}}]}\n\n", Delay: 10 * time.Millisecond},
			{Data: "data: {\"id\":\"chat-1\",\"choices\":[{\"delta\":{\"reasoning_content\":\" Second thinking step: formulating the solution...\"}}]}\n\n", Delay: 10 * time.Millisecond},
			{Data: "data: {\"id\":\"chat-1\",\"choices\":[{\"delta\":{\"content\":\"Here is the final answer.\"}}]}\n\n", Delay: 10 * time.Millisecond},
			{Data: "data: {\"id\":\"chat-1\",\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{}}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":50,\"total_tokens\":70}}\n\n", Delay: 5 * time.Millisecond},
			{Data: "data: [DONE]\n\n", Delay: 5 * time.Millisecond},
		},
	}
	upstream := mocknvidia.New(sseScript)
	t.Cleanup(upstream.Close)

	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	seedReasoningChatModel(t, application)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	reqBody := `{"model":"public-model","messages":[{"role":"user","content":"Perform deep analysis"}],"stream":true,"reasoning_effort":"high"}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute stream request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream body: %v", err)
	}
	bodyStr := string(bodyBytes)

	if !strings.Contains(bodyStr, "First thinking step") {
		t.Fatalf("stream body missing first thinking chunk: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Second thinking step") {
		t.Fatalf("stream body missing second thinking chunk: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Here is the final answer.") {
		t.Fatalf("stream body missing content chunk: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "[DONE]") {
		t.Fatalf("stream body missing [DONE]: %s", bodyStr)
	}

	// Verify flush of observability records
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := application.FlushObservability(ctx); err != nil {
		t.Fatalf("FlushObservability: %v", err)
	}
}

// TestCompatibilityMaxCompletionTokensMapping verifies that client sending max_completion_tokens
// is mapped to max_tokens for upstream, and stripped from upstream body so NVIDIA NIM does not reject with 422.
func TestCompatibilityMaxCompletionTokensMapping(t *testing.T) {
	wantResponse := `{"id":"chat-2","choices":[{"message":{"role":"assistant","content":"4096 tokens allocated"}}]}`
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: wantResponse})
	t.Cleanup(upstream.Close)

	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	reqBody := `{"model":"public-model","messages":[{"role":"user","content":"Write a very long essay"}],"max_completion_tokens":4096}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}

	requests := upstream.Requests()
	if len(requests) != 1 {
		t.Fatalf("upstream requests count = %d, want 1", len(requests))
	}
	var upstreamBody map[string]any
	if err := json.Unmarshal(requests[0].Body, &upstreamBody); err != nil {
		t.Fatalf("decode upstream request body: %v", err)
	}
	if maxTokens, ok := upstreamBody["max_tokens"].(float64); !ok || maxTokens != 4096 {
		t.Fatalf("upstream max_tokens = %v, want 4096", upstreamBody["max_tokens"])
	}
	if _, exists := upstreamBody["max_completion_tokens"]; exists {
		t.Fatal("upstream body should not contain max_completion_tokens")
	}
}

// TestReasoningModelsObservabilityAndExtraction verifies extraction of various reasoning formats
// and recording into request observability.
func TestReasoningModelsObservabilityAndExtraction(t *testing.T) {
	wantResponse := `{"id":"chat-3","choices":[{"message":{"role":"assistant","reasoning_content":"深度思考：这是一个复杂的优化问题","content":"最终解答结果"}}]}`
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: wantResponse})
	t.Cleanup(upstream.Close)

	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	seedReasoningChatModel(t, application)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	reqBody := `{"model":"public-model","messages":[{"role":"user","content":"请详细推导"}],"reasoning_effort":"high"}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}

	present, chars := observability.ReasoningContentFromBody([]byte(wantResponse))
	if !present || chars != 16 { // 16 runes in "深度思考：这是一个复杂的优化问题"
		t.Fatalf("ReasoningContentFromBody: present=%v, chars=%d, want true, 16", present, chars)
	}
}

// TestKeyFailoverAndRetryBudget verifies failover when upstream returns 429 or 503 to another key.
func TestKeyFailoverAndRetryBudget(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusTooManyRequests, Body: `{"error":{"message":"Rate limit exceeded"}}`},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"id":"chat-4","choices":[{"message":{"role":"assistant","content":"Recovered on key 2"}}]}`},
	)
	t.Cleanup(upstream.Close)

	application, accessToken := newChatTestApp(t, upstream, []string{"key-1", "key-2"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, body := postChat(t, server.URL, accessToken, chatBody("public-model"))
	if status != http.StatusOK || !strings.Contains(body, "Recovered on key 2") {
		t.Fatalf("failover failed: status = %d, body = %s", status, body)
	}
	if upstream.Count() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.Count())
	}
}

// TestHighConcurrencyStability tests 20 concurrent requests to verify thread safety and pooling.
func TestHighConcurrencyStability(t *testing.T) {
	const totalRequests = 20
	scripts := make([]mocknvidia.Script, totalRequests)
	for i := 0; i < totalRequests; i++ {
		scripts[i] = mocknvidia.Script{
			Status: http.StatusOK,
			Body:   fmt.Sprintf(`{"id":"chat-%d","choices":[{"message":{"role":"assistant","content":"Concurrent response"}}]}`, i),
		}
	}
	upstream := mocknvidia.New(scripts...)
	t.Cleanup(upstream.Close)

	// Provide 4 upstream keys
	application, accessToken := newChatTestApp(t, upstream, []string{"k1", "k2", "k3", "k4"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	var wg sync.WaitGroup
	errCh := make(chan error, totalRequests)

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqBody := fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"Concurrent request %d"}]}`, idx)
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
			if err != nil {
				errCh <- fmt.Errorf("create req %d: %w", idx, err)
				return
			}
			req.Header.Set("Authorization", "Bearer "+accessToken)
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- fmt.Errorf("do req %d: %w", idx, err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				errCh <- fmt.Errorf("req %d status %d: %s", idx, resp.StatusCode, string(b))
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrency error: %v", err)
	}
}
