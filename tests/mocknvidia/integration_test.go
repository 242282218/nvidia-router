package mocknvidia_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/app"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/nvidiakey"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/tests/mocknvidia"
)

const (
	publicChatModel   = "public-chat"
	upstreamChatModel = "vendor/chat"
)

type appHarness struct {
	application *app.App
	server      *httptest.Server
	upstream    *mocknvidia.Server
	accessToken string
	db          *sql.DB
	dbPath      string
	keyIDs      []int64
	modelID     int64
}

func TestUnknownChatFieldsReachNVIDIAUnchanged(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[]}`})
	harness := newAppHarness(t, upstream, []string{"nvapi-integration-1"})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat",
		"messages":[{"role":"user","content":"hello"}],
		"future_option":{"enabled":true}
	}`)
	if status != http.StatusOK {
		t.Fatalf("response = %d %s", status, body)
	}
	requests := upstream.Requests()
	if len(requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(requests))
	}
	if !strings.Contains(string(requests[0].Body), `"future_option":{"enabled":true}`) {
		t.Fatalf("upstream body lost unknown field: %s", requests[0].Body)
	}
	if !strings.Contains(string(requests[0].Body), `"model":"vendor/chat"`) {
		t.Fatalf("upstream body did not map model: %s", requests[0].Body)
	}
}

func TestResponseHeaderTimeoutFailsOverToNextKey(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"too-late"}}]}`, Delay: 1500 * time.Millisecond},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"fallback"}}]}`},
	)
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream: upstream,
		secrets:  []string{"nvapi-header-1", "nvapi-header-2"},
		settings: runtimeconfig.Snapshot{
			QueueCapacity: 10, QueueWaitTimeoutMS: 1000, ConnectTimeoutMS: 1000,
			FirstByteTimeoutMS: 1000, NonstreamTotalTimeoutMS: 4000, ShutdownGraceMS: 1000,
		},
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat","messages":[{"role":"user","content":"hello"}]
	}`)
	if status != http.StatusOK || !strings.Contains(body, "fallback") || strings.Contains(body, "too-late") {
		t.Fatalf("response = %d %s", status, body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "nvapi-header-1", "nvapi-header-2")
}

func TestChatFirstEventTimeoutFailsOverBeforeCommit(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{
			Status:       http.StatusOK,
			FlushHeaders: true,
			SSE: []mocknvidia.SSEChunk{{
				Data:        "data: {\"choices\":[{\"delta\":{\"content\":\"too-late\"}}]}\n\n",
				Delay:       1500 * time.Millisecond,
				NoKeepAlive: true,
			}},
		},
		mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
			{Data: "data: {\"choices\":[{\"delta\":{\"content\":\"fallback\"}}]}\n\n"},
			{Data: "data: [DONE]\n\n"},
		}},
	)
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream: upstream,
		secrets:  []string{"nvapi-timeout-1", "nvapi-timeout-2"},
		settings: runtimeconfig.Snapshot{
			QueueCapacity: 10, QueueWaitTimeoutMS: 1000, ConnectTimeoutMS: 1000,
			FirstByteTimeoutMS: 1000, NonstreamTotalTimeoutMS: 3000, ShutdownGraceMS: 1000,
		},
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat","messages":[{"role":"user","content":"hello"}],"stream":true
	}`)
	if status != http.StatusOK || !strings.Contains(body, "fallback") || strings.Contains(body, "too-late") {
		t.Fatalf("response = %d %s", status, body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "nvapi-timeout-1", "nvapi-timeout-2")
}

func TestRetryableStatusMatrix(t *testing.T) {
	tests := []struct {
		status  int
		headers http.Header
	}{
		{status: http.StatusUnauthorized},
		{status: http.StatusForbidden},
		{status: http.StatusTooManyRequests, headers: http.Header{"Retry-After": []string{"2"}}},
		{status: http.StatusInternalServerError},
		{status: http.StatusBadGateway},
		{status: http.StatusServiceUnavailable},
		{status: http.StatusGatewayTimeout},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("status_%d", test.status), func(t *testing.T) {
			firstSecret := fmt.Sprintf("nvapi-status-%d-first", test.status)
			secondSecret := fmt.Sprintf("nvapi-status-%d-second", test.status)
			upstream := mocknvidia.New(
				mocknvidia.Script{Status: test.status, Headers: test.headers, Body: `{"error":{"message":"private upstream fixture"}}`},
				mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"fallback"}}]}`},
			)
			harness := newAppHarness(t, upstream, []string{firstSecret, secondSecret})

			status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
				"model":"public-chat","messages":[{"role":"user","content":"hello"}]
			}`)
			if status != http.StatusOK || !strings.Contains(body, "fallback") {
				t.Fatalf("response = %d %s", status, body)
			}
			assertAuthorizationOrder(t, upstream.Requests(), firstSecret, secondSecret)
			assertStatusSideEffect(t, harness, test.status)
		})
	}
}

func TestConnectionFailureFailsOverToNextKey(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Disconnect: true},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"fallback"}}]}`},
	)
	harness := newAppHarness(t, upstream, []string{"nvapi-connect-1", "nvapi-connect-2"})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat","messages":[{"role":"user","content":"hello"}]
	}`)
	if status != http.StatusOK || !strings.Contains(body, "fallback") {
		t.Fatalf("response = %d %s", status, body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "nvapi-connect-1", "nvapi-connect-2")
}

func TestNonstreamTotalTimeoutDoesNotStartAnotherKey(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{
			Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"too-late"}}]}`,
			FlushHeaders: true, BodyDelay: 1500 * time.Millisecond,
		},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"must-not-run"}}]}`},
	)
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream: upstream,
		secrets:  []string{"nvapi-total-1", "nvapi-total-2"},
		settings: runtimeconfig.Snapshot{
			QueueCapacity: 10, QueueWaitTimeoutMS: 1000, ConnectTimeoutMS: 1000,
			FirstByteTimeoutMS: 3000, NonstreamTotalTimeoutMS: 1000, ShutdownGraceMS: 1000,
		},
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat","messages":[{"role":"user","content":"hello"}]
	}`)
	if status != http.StatusGatewayTimeout || !strings.Contains(body, `"code":"upstream_timeout"`) {
		t.Fatalf("response = %d %s", status, body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "nvapi-total-1")
}

func TestRetryableFailuresTraverseEveryKeyOnce(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusInternalServerError},
		mocknvidia.Script{Status: http.StatusBadGateway},
		mocknvidia.Script{Status: http.StatusServiceUnavailable},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"last-key"}}]}`},
	)
	secrets := []string{"nvapi-walk-1", "nvapi-walk-2", "nvapi-walk-3", "nvapi-walk-4"}
	harness := newAppHarness(t, upstream, secrets)

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat","messages":[{"role":"user","content":"hello"}]
	}`)
	if status != http.StatusOK || !strings.Contains(body, "last-key") {
		t.Fatalf("response = %d %s", status, body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), secrets...)
}

func TestPoolStateBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *sql.DB, []int64, int64)
		status  int
		code    string
	}{
		{
			name: "all_disabled", status: http.StatusServiceUnavailable, code: "no_available_keys",
			prepare: func(t *testing.T, db *sql.DB, _ []int64, _ int64) {
				mustExec(t, db, `UPDATE nvidia_keys SET enabled = 0`)
			},
		},
		{
			name: "all_cooling", status: http.StatusTooManyRequests, code: "all_keys_cooling_down",
			prepare: func(t *testing.T, db *sql.DB, _ []int64, _ int64) {
				mustExec(t, db, `UPDATE nvidia_keys SET cooldown_until = ?`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			},
		},
		{
			name: "all_model_blocked", status: http.StatusNotFound, code: "model_not_available",
			prepare: func(t *testing.T, db *sql.DB, keyIDs []int64, modelID int64) {
				now := time.Now().UTC().Format(time.RFC3339)
				for _, keyID := range keyIDs {
					mustExec(t, db, `INSERT INTO nvidia_key_model_blocks
						(nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at)
						VALUES (?, ?, 'model_not_available', 403, ?, ?)`, keyID, modelID, now, now)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[]}`})
			harness := newAppHarnessWithOptions(t, harnessOptions{
				upstream: upstream, secrets: []string{"nvapi-state-1", "nvapi-state-2"}, prepare: test.prepare,
			})

			status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
				"model":"public-chat","messages":[{"role":"user","content":"hello"}]
			}`)
			if status != test.status || !strings.Contains(body, `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d %s", status, body)
			}
			if upstream.Count() != 0 {
				t.Fatalf("upstream request count = %d, want 0", upstream.Count())
			}
		})
	}
}

func TestCooldownRecovery(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusTooManyRequests, Headers: http.Header{"Retry-After": []string{"1"}}},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"recovered"}}]}`},
	)
	harness := newAppHarness(t, upstream, []string{"nvapi-cooldown"})
	requestBody := `{"model":"public-chat","messages":[{"role":"user","content":"hello"}]}`

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", requestBody)
	if status != http.StatusTooManyRequests || !strings.Contains(body, `"code":"rate_limit_exceeded"`) {
		t.Fatalf("first response = %d %s", status, body)
	}
	status, body, _ = harness.request(t, http.MethodPost, "/v1/chat/completions", requestBody)
	if status != http.StatusTooManyRequests || !strings.Contains(body, `"code":"all_keys_cooling_down"`) {
		t.Fatalf("cooldown response = %d %s", status, body)
	}
	if upstream.Count() != 1 {
		t.Fatalf("upstream request count during cooldown = %d, want 1", upstream.Count())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, body, _ = harness.request(t, http.MethodPost, "/v1/chat/completions", requestBody)
		if status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("key did not recover: %d %s", status, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(body, "recovered") || upstream.Count() != 2 {
		t.Fatalf("recovered response = %d %s; upstream count=%d", status, body, upstream.Count())
	}
}

func TestUnknownInterfaceAndImagePayloadBoundaries(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[]}`})
	harness := newAppHarness(t, upstream, []string{"nvapi-image"})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/unknown", `{}`)
	if status != http.StatusNotImplemented || !strings.Contains(body, `"code":"not_implemented"`) {
		t.Fatalf("unknown endpoint response = %d %s", status, body)
	}
	status, body, _ = harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"public-chat","messages":[{"role":"user","content":[
			{"type":"text","text":"inspect"},
			{"type":"image_url","image_url":{"url":"https://example.invalid/image.png"}},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}
		]}]
	}`)
	if status != http.StatusOK {
		t.Fatalf("image response = %d %s", status, body)
	}
	requests := upstream.Requests()
	if len(requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(requests))
	}
	upstreamBody := string(requests[0].Body)
	for _, value := range []string{"https://example.invalid/image.png", "data:image/png;base64,iVBORw0KGgo=", `"model":"vendor/chat"`} {
		if !strings.Contains(upstreamBody, value) {
			t.Fatalf("upstream body missing %q: %s", value, upstreamBody)
		}
	}
}

func TestAudioMultipartTemporaryFilesAreRemovedOnEveryExit(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)
	upstream := mocknvidia.New()
	harness := newAppHarness(t, upstream, []string{"nvapi-audio-cleanup"})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "public-asr"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "oversized.wav")
	if err != nil {
		t.Fatalf("create audio part: %v", err)
	}
	if _, err := part.Write(make([]byte, (25<<20)+1)); err != nil {
		t.Fatalf("write oversized audio: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	request.Header.Set("Authorization", "Bearer "+harness.accessToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	harness.application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), `"code":"request_too_large"`) {
		t.Fatalf("audio response = %d %s", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatalf("read multipart temp directory: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("multipart temporary files remain after request: %v", names)
	}
}

func TestQueueFullTimeoutAndSingleKeyConcurrency(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{
		Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"holder"}}]}`, Delay: 1800 * time.Millisecond,
	})
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream: upstream, secrets: []string{"nvapi-queue"},
		settings: runtimeconfig.Snapshot{
			QueueCapacity: 1, QueueWaitTimeoutMS: 1000, ConnectTimeoutMS: 1000,
			FirstByteTimeoutMS: 3000, NonstreamTotalTimeoutMS: 5000, ShutdownGraceMS: 1000,
		},
	})
	requestBody := `{"model":"public-chat","messages":[{"role":"user","content":"hello"}]}`

	first := requestAsync(harness, requestBody)
	waitFor(t, 2*time.Second, func() bool { return upstream.Count() == 1 }, "first request to occupy the NVIDIA key")
	second := requestAsync(harness, requestBody)
	time.Sleep(100 * time.Millisecond)
	third := receiveHTTP(t, requestAsync(harness, requestBody), 2*time.Second)
	if third.status != http.StatusTooManyRequests || !strings.Contains(third.body, `"code":"queue_full"`) {
		t.Fatalf("third response = %d %s err=%v", third.status, third.body, third.err)
	}
	queued := receiveHTTP(t, second, 2*time.Second)
	if queued.status != http.StatusTooManyRequests || !strings.Contains(queued.body, `"code":"queue_timeout"`) {
		t.Fatalf("queued response = %d %s err=%v", queued.status, queued.body, queued.err)
	}
	holder := receiveHTTP(t, first, 3*time.Second)
	if holder.status != http.StatusOK || !strings.Contains(holder.body, "holder") {
		t.Fatalf("holder response = %d %s err=%v", holder.status, holder.body, holder.err)
	}
	if upstream.Count() != 1 || upstream.MaxActive() != 1 {
		t.Fatalf("upstream count=%d max_active=%d, want 1 and 1", upstream.Count(), upstream.MaxActive())
	}
}

func TestChatSSEPassthroughAndCommitBoundaries(t *testing.T) {
	t.Run("non_json_and_duplicate_done", func(t *testing.T) {
		upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
			{Data: ": vendor-comment\n\n"},
			{Data: "event: vendor.extension\ndata: not-json\n\n"},
			{Data: "data: [DONE]\n\n"},
			{Data: "data: [DONE]\n\n"},
		}})
		harness := newAppHarness(t, upstream, []string{"nvapi-sse-nonjson"})

		status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
			"model":"public-chat","messages":[{"role":"user","content":"hello"}],"stream":true
		}`)
		if status != http.StatusOK || !strings.Contains(body, "event: vendor.extension") || !strings.Contains(body, "data: not-json") {
			t.Fatalf("response = %d %s", status, body)
		}
		if strings.Count(body, "data: [DONE]") != 1 {
			t.Fatalf("[DONE] count = %d, want 1; body=%s", strings.Count(body, "data: [DONE]"), body)
		}
	})

	t.Run("interrupted_after_commit_does_not_retry", func(t *testing.T) {
		upstream := mocknvidia.New(
			mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{{Data: "data: partial-extension\n\n"}}},
			mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{{Data: "data: must-not-run\n\n"}, {Data: "data: [DONE]\n\n"}}},
		)
		harness := newAppHarness(t, upstream, []string{"nvapi-commit-1", "nvapi-commit-2"})

		status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
			"model":"public-chat","messages":[{"role":"user","content":"hello"}],"stream":true
		}`)
		if status != http.StatusOK || !strings.Contains(body, "partial-extension") || strings.Contains(body, "must-not-run") {
			t.Fatalf("response = %d %s", status, body)
		}
		assertAuthorizationOrder(t, upstream.Requests(), "nvapi-commit-1")
	})
}

func TestClientCancellationStopsCommittedStreamWithoutRetry(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
			{Data: "data: first-event\n\n"},
			{Data: "data: late-event\n\n", Delay: 5 * time.Second},
		}},
		mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{{Data: "data: must-not-run\n\n"}}},
	)
	harness := newAppHarness(t, upstream, []string{"nvapi-cancel-1", "nvapi-cancel-2"})
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, harness.server.URL+"/v1/chat/completions", strings.NewReader(`{
		"model":"public-chat","messages":[{"role":"user","content":"hello"}],"stream":true
	}`))
	if err != nil {
		t.Fatalf("create cancellation request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+harness.accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("start cancellation request: %v", err)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "first-event") {
		_ = response.Body.Close()
		t.Fatalf("first stream line = %q err=%v", line, err)
	}
	cancel()
	_ = response.Body.Close()

	waitFor(t, 2*time.Second, func() bool { return upstream.CanceledCount() > 0 }, "upstream stream cancellation")
	assertAuthorizationOrder(t, upstream.Requests(), "nvapi-cancel-1")
}

func TestResponsesEventsToolArgumentsAndStoreRejection(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
		{Data: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"fc_1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\":\\\"Paris\\\"}\"}}]}}]}\n\n"},
		{Data: "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"},
		{Data: "data: [DONE]\n\n"},
	}})
	harness := newAppHarness(t, upstream, []string{"nvapi-responses"})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/responses", `{
		"model":"public-chat","input":"find weather","stream":true,
		"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup","parameters":{"type":"object"}}}]
	}`)
	if status != http.StatusOK {
		t.Fatalf("responses stream = %d %s", status, body)
	}
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.function_call_arguments.done",
		`"arguments":"{\"city\":\"Paris\"}"`,
		"event: response.completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("responses stream missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("responses [DONE] count = %d, want 1", strings.Count(body, "data: [DONE]"))
	}

	before := upstream.Count()
	status, body, _ = harness.request(t, http.MethodPost, "/v1/responses", `{
		"model":"public-chat","input":"hello","store":true
	}`)
	if status != http.StatusBadRequest || !strings.Contains(body, `"code":"unsupported_responses_feature"`) {
		t.Fatalf("store:true response = %d %s", status, body)
	}
	if upstream.Count() != before {
		t.Fatalf("store:true reached upstream: before=%d after=%d", before, upstream.Count())
	}
}

func TestResponsesCommittedMalformedStreamFailsWithoutRetry(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
			{Data: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"},
			{Data: "data: {not-json}\n\n"},
		}},
		mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
			{Data: "data: must-not-run\n\n"},
			{Data: "data: [DONE]\n\n"},
		}},
	)
	harness := newAppHarness(t, upstream, []string{"nvapi-responses-bad", "nvapi-responses-second"})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/responses", `{
		"model":"public-chat","input":"hello","stream":true
	}`)
	if status != http.StatusOK {
		t.Fatalf("responses stream = %d %s", status, body)
	}
	if !strings.Contains(body, "event: response.failed") || strings.Contains(body, "event: response.completed") {
		t.Fatalf("unexpected terminal events:\n%s", body)
	}
	if strings.Count(body, "event: response.failed") != 1 || strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("terminal counts failed=%d done=%d:\n%s", strings.Count(body, "event: response.failed"), strings.Count(body, "data: [DONE]"), body)
	}
	if strings.Contains(body, "must-not-run") {
		t.Fatalf("second key response leaked into stream:\n%s", body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "nvapi-responses-bad")
}

func assertStatusSideEffect(t *testing.T, harness *appHarness, status int) {
	t.Helper()
	switch status {
	case http.StatusUnauthorized:
		var invalid int
		if err := harness.db.QueryRow(`SELECT auth_invalid FROM nvidia_keys WHERE id = ?`, harness.keyIDs[0]).Scan(&invalid); err != nil {
			t.Fatalf("load auth invalid state: %v", err)
		}
		if invalid != 1 {
			t.Fatalf("auth_invalid = %d, want 1", invalid)
		}
	case http.StatusForbidden:
		var count int
		if err := harness.db.QueryRow(`SELECT COUNT(*) FROM nvidia_key_model_blocks WHERE nvidia_key_id = ? AND model_id = ?`, harness.keyIDs[0], harness.modelID).Scan(&count); err != nil {
			t.Fatalf("load model block: %v", err)
		}
		if count != 1 {
			t.Fatalf("model block count = %d, want 1", count)
		}
	case http.StatusTooManyRequests:
		var raw string
		if err := harness.db.QueryRow(`SELECT cooldown_until FROM nvidia_keys WHERE id = ?`, harness.keyIDs[0]).Scan(&raw); err != nil {
			t.Fatalf("load cooldown: %v", err)
		}
		until, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			t.Fatalf("parse cooldown %q: %v", raw, err)
		}
		remaining := time.Until(until)
		if remaining < time.Second || remaining > 3*time.Second {
			t.Fatalf("Retry-After cooldown remaining = %v, want about 2s", remaining)
		}
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execute fixture SQL: %v", err)
	}
}

type harnessOptions struct {
	upstream *mocknvidia.Server
	secrets  []string
	settings runtimeconfig.Snapshot
	logger   *slog.Logger
	prepare  func(*testing.T, *sql.DB, []int64, int64)
}

func newAppHarness(t *testing.T, upstream *mocknvidia.Server, secrets []string) *appHarness {
	t.Helper()
	return newAppHarnessWithOptions(t, harnessOptions{upstream: upstream, secrets: secrets})
}

func newAppHarnessWithOptions(t *testing.T, options harnessOptions) *appHarness {
	t.Helper()
	upstream := options.upstream
	secrets := options.secrets
	t.Cleanup(upstream.Close)
	root := t.TempDir()
	dbPath := filepath.Join(root, "router.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	appOwnsDB := false
	t.Cleanup(func() {
		if !appOwnsDB {
			_ = db.Close()
		}
	})

	masterKey := [32]byte{30}
	keys, err := crypto.New(masterKey)
	if err != nil {
		t.Fatalf("create key set: %v", err)
	}
	accessService := accesskey.NewService(accesskey.NewRepository(db), keys, clock.RealClock{})
	created, err := accessService.Create(context.Background(), "integration")
	if err != nil {
		t.Fatalf("create access key: %v", err)
	}
	keyIDs := seedKeys(t, db, keys, secrets)
	modelID := seedChatModel(t, db)
	if options.prepare != nil {
		options.prepare(t, db, keyIDs, modelID)
	}
	if options.settings != (runtimeconfig.Snapshot{}) {
		storeRuntimeSettings(t, db, options.settings)
	}

	baseURL, err := url.Parse(upstream.URL())
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	logger := options.logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	application, err := app.New(context.Background(), app.Dependencies{
		Config: config.Config{DataDir: root, MasterKey: masterKey, NVIDIABaseURL: baseURL},
		DB:     db, Logger: logger, Clock: clock.RealClock{},
		NVIDIAHTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := db.Exec("UPDATE admins SET must_change_password = 0 WHERE id = 1"); err != nil {
		t.Fatalf("complete initial password change: %v", err)
	}
	appOwnsDB = true
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	return &appHarness{application: application, server: server, upstream: upstream, accessToken: created.Plaintext, db: db, dbPath: dbPath, keyIDs: keyIDs, modelID: modelID}
}

func seedKeys(t *testing.T, db *sql.DB, keys *crypto.KeySet, secrets []string) []int64 {
	t.Helper()
	repository := nvidiakey.NewRepository(db)
	ids := make([]int64, 0, len(secrets))
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	for index, secret := range secrets {
		ciphertext, nonce, err := keys.Encrypt([]byte(secret), "nvidia-key:v1")
		if err != nil {
			t.Fatalf("encrypt NVIDIA key: %v", err)
		}
		created, duplicate, err := repository.Create(context.Background(), ciphertext, nonce, []byte{byte(index + 1)}, "test", "key", now)
		if err != nil || duplicate {
			t.Fatalf("create NVIDIA key %d: duplicate=%v err=%v", index, duplicate, err)
		}
		ids = append(ids, created.ID)
	}
	return ids
}

func seedChatModel(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: publicChatModel, UpstreamID: upstreamChatModel, DisplayName: "Integration Chat",
		Kind: modelcatalog.KindChat, Enabled: true, SupportsVision: true, SupportsTools: true,
		ReasoningWireFormat: "none",
	}}, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("save chat model: %v", err)
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM models WHERE public_id = ?`, publicChatModel).Scan(&id); err != nil {
		t.Fatalf("load chat model ID: %v", err)
	}
	return id
}

func (h *appHarness) request(t *testing.T, method, path, body string) (int, string, http.Header) {
	t.Helper()
	result := h.doRequest(context.Background(), method, path, body, nil)
	if result.err != nil {
		t.Fatalf("perform request: %v", result.err)
	}
	return result.status, result.body, result.header
}

type httpResult struct {
	status int
	body   string
	header http.Header
	err    error
}

func (h *appHarness) doRequest(ctx context.Context, method, path, body string, extra http.Header) httpResult {
	request, err := http.NewRequestWithContext(ctx, method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		return httpResult{err: fmt.Errorf("create request: %w", err)}
	}
	request.Header.Set("Authorization", "Bearer "+h.accessToken)
	request.Header.Set("Content-Type", "application/json")
	for key, values := range extra {
		request.Header[key] = append([]string(nil), values...)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return httpResult{err: fmt.Errorf("send request: %w", err)}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return httpResult{err: fmt.Errorf("read response: %w", err)}
	}
	return httpResult{status: response.StatusCode, body: string(payload), header: response.Header.Clone()}
}

func requestAsync(harness *appHarness, body string) <-chan httpResult {
	result := make(chan httpResult, 1)
	go func() {
		result <- harness.doRequest(context.Background(), http.MethodPost, "/v1/chat/completions", body, nil)
	}()
	return result
}

func receiveHTTP(t *testing.T, result <-chan httpResult, timeout time.Duration) httpResult {
	t.Helper()
	select {
	case received := <-result:
		return received
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for HTTP response", timeout)
		return httpResult{}
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func storeRuntimeSettings(t *testing.T, db *sql.DB, settings runtimeconfig.Snapshot) {
	t.Helper()
	_, err := db.Exec(`
		UPDATE runtime_settings SET
			queue_capacity = ?, queue_wait_timeout_ms = ?, connect_timeout_ms = ?,
			first_byte_timeout_ms = ?, nonstream_total_timeout_ms = ?, shutdown_grace_ms = ?,
			updated_at = ?
		WHERE id = 1
	`, settings.QueueCapacity, settings.QueueWaitTimeoutMS, settings.ConnectTimeoutMS,
		settings.FirstByteTimeoutMS, settings.NonstreamTotalTimeoutMS, settings.ShutdownGraceMS,
		time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("store runtime settings: %v", err)
	}
}

func assertAuthorizationOrder(t *testing.T, requests []mocknvidia.Request, secrets ...string) {
	t.Helper()
	if len(requests) != len(secrets) {
		t.Fatalf("request count = %d, want %d", len(requests), len(secrets))
	}
	for index, secret := range secrets {
		if got := requests[index].Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("request %d Authorization = %q, want Bearer %s", index+1, got, secret)
		}
	}
}
