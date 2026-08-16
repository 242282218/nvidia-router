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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"ok"}}]}`})
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
			// The first-event window is its own setting since the 014 split:
			// the first SSE data event must arrive within this budget or the
			// attempt fails over before committing.
			StreamFirstTokenTimeoutMS: 1000,
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
		status       int
		headers      http.Header
		wantFailover bool
	}{
		// A credential 401 is per-key (R2.2): it must not fail over to the
		// healthy key; it disables the offending key instead.
		{status: http.StatusUnauthorized, wantFailover: false},
		{status: http.StatusForbidden, wantFailover: true},
		{status: http.StatusTooManyRequests, headers: http.Header{"Retry-After": []string{"2"}}, wantFailover: true},
		{status: http.StatusInternalServerError, wantFailover: true},
		{status: http.StatusBadGateway, wantFailover: true},
		{status: http.StatusServiceUnavailable, wantFailover: true},
		{status: http.StatusGatewayTimeout, wantFailover: true},
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
			if test.wantFailover {
				if status != http.StatusOK || !strings.Contains(body, "fallback") {
					t.Fatalf("response = %d %s", status, body)
				}
				assertAuthorizationOrder(t, upstream.Requests(), firstSecret, secondSecret)
			} else {
				if status != http.StatusUnauthorized {
					t.Fatalf("response = %d %s, want 401 (credential faults must not fail over)", status, body)
				}
				assertAuthorizationOrder(t, upstream.Requests(), firstSecret)
			}
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
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{"message":{"content":"ok"}}]}`})
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

func TestDeepSeekV4FlashReasoningStreamThroughRouter(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
		{Data: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"deep-thought\"}}]}\n\n"},
		{Data: "data: {\"choices\":[{\"delta\":{\"content\":\"final-answer\"}}]}\n\n"},
		{Data: "data: [DONE]\n\n"},
	}})
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream: upstream,
		secrets:  []string{"nvapi-ds-flash-1"},
		prepare: func(t *testing.T, db *sql.DB, _ []int64, _ int64) {
			if err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{{
				PublicID: "deepseek-ai/deepseek-v4-flash", UpstreamID: "deepseek-ai/deepseek-v4-flash",
				DisplayName: "DeepSeek V4 Flash", Kind: modelcatalog.KindChat, Enabled: true,
				SupportsReasoning: true, ReasoningWireFormat: "openai",
			}}, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Fatalf("save deepseek flash model: %v", err)
			}
		},
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", `{
		"model":"deepseek-ai/deepseek-v4-flash",
		"messages":[{"role":"user","content":"long task"}],
		"thinking":{"type":"enabled","budget_tokens":8192},
		"stream":true
	}`)
	if status != http.StatusOK {
		t.Fatalf("response = %d %s", status, body)
	}
	if !strings.Contains(body, "deep-thought") || !strings.Contains(body, "final-answer") {
		t.Fatalf("response body lost reasoning or content: %s", body)
	}
	requests := upstream.Requests()
	if len(requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(requests))
	}
	if !strings.Contains(string(requests[0].Body), `"thinking"`) {
		t.Fatalf("upstream body did not preserve native thinking: %s", requests[0].Body)
	}
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
		// The router honours the upstream Retry-After (2s) before switching keys
		// (R6), so by the time the fallback response returns the cooldown has
		// already elapsed. Assert the key is parked just about now — the cooldown
		// must not be 2s in the future (the Retry-After was ignored) nor long past
		// (the cooldown was not anchored to the 429).
		if remaining > time.Second || -remaining > 3*time.Second {
			t.Fatalf("Retry-After cooldown remaining = %v, want just-expired after the Retry-After backoff", remaining)
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

	// Proxy mode (星空代理池): when proxyURL is set the App is configured with
	// the static HTTP proxy-pool endpoint and all NVIDIA upstream traffic must
	// flow through the CONNECT proxy. The NVIDIA upstream
	// is then the TLS server created by newTLSNVIDIAUpstream instead of the
	// plain HTTP mocknvidia server, because CONNECT tunneling only applies to
	// HTTPS. tlsUpstream, when non-nil, is used as that TLS upstream so tests
	// can inspect its request log; otherwise the harness creates one.
	proxyURL     *url.URL
	proxyAuthKey string
	tlsUpstream  *tlsUpstreamFixture
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
	// Integration tests assert deterministic key-selection order (round-robin +
	// failover). Latency-aware scheduling introduces weighted randomness, so the
	// shared harness turns it off; the scheduler itself is covered by dedicated
	// pool unit tests.
	if _, err := db.Exec(`UPDATE runtime_settings SET latency_routing_enabled = 0 WHERE id = 1`); err != nil {
		t.Fatalf("disable latency routing in harness: %v", err)
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
	config := config.Config{DataDir: root, MasterKey: masterKey, InitialAdminPassword: "test-initial-admin-password", NVIDIABaseURL: baseURL}
	nvidiaHTTPClient := upstream.Client()
	if options.proxyURL != nil {
		// 星空代理池模式：使用本地 TLS NVIDIA 上游（CONNECT 只对 HTTPS 有意义），
		// 注入静态代理池端点。App 的 NVIDIAHTTPClient 使用 TLS 上游的 Client()，
		// 其 Transport 信任该 TLS server 证书。
		tlsUpstream := options.tlsUpstream
		if tlsUpstream == nil {
			tlsUpstream = newTLSNVIDIAUpstream(t)
		}
		baseURL, err = url.Parse(tlsUpstream.URL)
		if err != nil {
			t.Fatalf("parse TLS upstream URL: %v", err)
		}
		config.NVIDIABaseURL = baseURL
		nvidiaHTTPClient = tlsUpstream.Client()
		config.XKProxyURL = options.proxyURL
		config.XKProxyAuthKey = options.proxyAuthKey
		if config.XKProxyAuthKey == "" {
			config.XKProxyAuthKey = "proxy-secret"
		}
	}
	application, err := app.New(context.Background(), app.Dependencies{
		Config: config,
		DB:     db, Logger: logger, Clock: clock.RealClock{},
		NVIDIAHTTPClient: nvidiaHTTPClient,
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
	defer func() { _ = response.Body.Close() }()
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
	// The stream timeout split (migration 014) defaults to 60000/180000; only
	// overwrite a column when a test opts into a custom value, because 0 is
	// outside the CHECK range and means "unset" on the Snapshot.
	if settings.StreamFirstTokenTimeoutMS != 0 {
		mustExec(t, db, `UPDATE runtime_settings SET stream_first_token_timeout_ms = ? WHERE id = 1`, settings.StreamFirstTokenTimeoutMS)
	}
	if settings.StreamIdleTimeoutMS != 0 {
		mustExec(t, db, `UPDATE runtime_settings SET stream_idle_timeout_ms = ? WHERE id = 1`, settings.StreamIdleTimeoutMS)
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

// ---------------------------------------------------------------------------
// 星空代理池端到端 fixture
//
// 链路：App（静态代理池 URL）→ HTTP CONNECT 代理 → TLS NVIDIA 上游。
// CONNECT 代理和 TLS 上游都是真实本地组件；App 的 NVIDIAHTTPClient 信任
// httptest.NewTLSServer 自建的证书。
// ---------------------------------------------------------------------------

type tlsUpstreamRequest struct {
	Method        string
	Path          string
	Authorization string
	ContentType   string
	Body          string
}

type tlsUpstreamFixture struct {
	*httptest.Server
	mu       sync.Mutex
	requests []tlsUpstreamRequest
	// sseDelay delays the first SSE chunk after the response headers. Set it in
	// tests that need a deterministic "headers arrived, first token pending"
	// window (e.g. reproducing the proxy streaming context-cancel bug).
	sseDelay time.Duration
	// chatAnswerByKey overrides the non-stream /v1/chat/completions response for
	// a request whose Authorization matches the map key exactly. A key without an
	// entry keeps answering 200. Used to throttle one specific key (429/529)
	// while the fallback key stays healthy in the proxy retry tests.
	chatAnswerByKey map[string]tlsUpstreamChatAnswer
}

type tlsUpstreamChatAnswer struct {
	status     int
	retryAfter string
}

// throttleKey makes every non-stream chat request carrying exactly this
// Authorization answer with status, plus retryAfter as the Retry-After header
// when non-empty. The router must then switch to a different key while the
// client transport itself must not replay the request (a transport replay would
// re-send the same throttled key and hit the same status again).
func (f *tlsUpstreamFixture) throttleKey(authorization string, status int, retryAfter string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.chatAnswerByKey == nil {
		f.chatAnswerByKey = make(map[string]tlsUpstreamChatAnswer)
	}
	f.chatAnswerByKey[authorization] = tlsUpstreamChatAnswer{status: status, retryAfter: retryAfter}
}

func (f *tlsUpstreamFixture) chatAnswer(authorization string) (tlsUpstreamChatAnswer, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	answer, ok := f.chatAnswerByKey[authorization]
	return answer, ok
}

// newTLSNVIDIAUpstream starts a minimal NVIDIA-compatible HTTPS upstream. Its
// /v1/models, /v1/chat/completions, /v1/embeddings and /v1/audio/* endpoints
// return valid JSON so every public v1 endpoint can be exercised end to end.
func newTLSNVIDIAUpstream(t *testing.T) *tlsUpstreamFixture {
	t.Helper()
	fixture := &tlsUpstreamFixture{}
	fixture.Server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body []byte
		if request.Body != nil {
			body, _ = io.ReadAll(request.Body)
		}
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, tlsUpstreamRequest{
			Method:        request.Method,
			Path:          request.URL.Path,
			Authorization: request.Header.Get("Authorization"),
			ContentType:   request.Header.Get("Content-Type"),
			Body:          string(body),
		})
		fixture.mu.Unlock()
		switch request.URL.Path {
		case "/v1/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"data":[{"id":"vendor/chat"},{"id":"vendor/embed"},{"id":"vendor/asr"},{"id":"vendor/tts"}]}`)
		case "/v1/chat/completions":
			if bytes.Contains(body, []byte(`"stream":true`)) {
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(http.StatusOK)
				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
				if fixture.sseDelay > 0 {
					time.Sleep(fixture.sseDelay)
				}
				// DeepSeek-style reasoning streams: when the client asked for a
				// reasoning model (normalized thinking -> reasoning_effort), emit a
				// reasoning_content delta before the answer content so the proxy
				// relay path is exercised end to end.
				if bytes.Contains(body, []byte(`"thinking"`)) || bytes.Contains(body, []byte(`"reasoning_effort"`)) {
					_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"proxied-reasoning\"}}]}\n\n")
					if flusher, ok := writer.(http.Flusher); ok {
						flusher.Flush()
					}
				}
				_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"proxied-stream\"}}]}\n\n")
				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
				_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				return
			}
			if answer, throttled := fixture.chatAnswer(request.Header.Get("Authorization")); throttled {
				writer.Header().Set("Content-Type", "application/json")
				if answer.retryAfter != "" {
					writer.Header().Set("Retry-After", answer.retryAfter)
				}
				writer.WriteHeader(answer.status)
				_, _ = io.WriteString(writer, `{"error":{"message":"upstream throttled"}}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"proxied-ok"}}],"usage":{"total_tokens":1}}`)
		case "/v1/embeddings":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"data":[{"embedding":[0.1,0.2]}],"usage":{"total_tokens":1}}`)
		case "/v1/audio/transcriptions":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"text":"proxied-transcript"}`)
		case "/v1/audio/speech":
			writer.Header().Set("Content-Type", "audio/wav")
			_, _ = writer.Write(probeWAVBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(fixture.Close)
	return fixture
}

func (f *tlsUpstreamFixture) requestsSnapshot() []tlsUpstreamRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tlsUpstreamRequest(nil), f.requests...)
}

func (f *tlsUpstreamFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// proxyFixture bundles the local HTTP CONNECT proxy and TLS NVIDIA upstream
// used to exercise the static proxy-pool integration.
type proxyFixture struct {
	connectProxy *connectProxyFixture
	upstream     *tlsUpstreamFixture
}

// newProxyFixture wires a TLS NVIDIA upstream and an HTTP CONNECT proxy.
func newProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()
	upstream := newTLSNVIDIAUpstream(t)
	fixture := &proxyFixture{upstream: upstream}
	fixture.connectProxy = newConnectProxyFixture(t, upstream.Listener.Addr().String())
	return fixture
}

func (f *proxyFixture) proxyURL(t *testing.T) *url.URL {
	t.Helper()
	parsed, err := url.Parse("http://" + f.connectProxy.address())
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	return parsed
}

func (f *proxyFixture) connectCount() int32 { return f.connectProxy.connects.Load() }

func (f *proxyFixture) failNextConnect() { f.connectProxy.failNext.Store(1) }

// failNextConnectWithStatus makes the next CONNECT answer with an HTTP status,
// which the client surfaces as a transport error that must NOT be replayed
// (R2.2: a 5xx proxy answer means the proxy is up and already refused).
func (f *proxyFixture) failNextConnectWithStatus(status int) {
	f.connectProxy.failNextStatus.Store(int64(status))
}

// connectProxyFixture is a minimal HTTP CONNECT proxy. Every CONNECT is counted
// and the tunnel is forwarded to the configured target host.
type connectProxyFixture struct {
	listener       net.Listener
	server         *http.Server
	target         string
	connects       atomic.Int32
	failNext       atomic.Int32
	failNextStatus atomic.Int64
}

func newConnectProxyFixture(t *testing.T, target string) *connectProxyFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen connect proxy: %v", err)
	}
	proxy := &connectProxyFixture{listener: listener, target: target}
	proxy.server = &http.Server{Handler: http.HandlerFunc(proxy.handle)}
	go func() { _ = proxy.server.Serve(listener) }()
	t.Cleanup(func() {
		_ = proxy.server.Close()
		_ = listener.Close()
	})
	return proxy
}

func (p *connectProxyFixture) address() string { return p.listener.Addr().String() }

func (p *connectProxyFixture) handle(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Proxy-Authorization") != "Basic cHJveHk6cHJveHktc2VjcmV0" {
		http.Error(writer, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if request.Method != http.MethodConnect {
		http.Error(writer, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	p.connects.Add(1)
	if p.failNext.CompareAndSwap(1, 0) {
		// A raw transport-level failure: accept the tunnel then drop it before
		// any HTTP response, so the client sees a genuine connection error that
		// is worth one replay.
		client, buffered, err := hijackProxyConn(writer)
		if err != nil {
			return
		}
		_ = buffered.Flush()
		_ = client.Close()
		return
	}
	if status := p.failNextStatus.Load(); status != 0 {
		p.failNextStatus.Store(0)
		http.Error(writer, "temporary proxy failure", int(status))
		return
	}
	upstream, err := net.DialTimeout("tcp", p.target, time.Second)
	if err != nil {
		http.Error(writer, "upstream unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(writer, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		_ = client.Close()
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	_ = upstream.Close()
}

func hijackProxyConn(writer http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("writer does not support hijacking")
	}
	return hijacker.Hijack()
}

var probeWAVBytes = []byte{
	'R', 'I', 'F', 'F', 0x26, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E',
	'f', 'm', 't', ' ', 0x10, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00,
	0x40, 0x1f, 0x00, 0x00, 0x80, 0x3e, 0x00, 0x00, 0x02, 0x00, 0x10, 0x00,
	'd', 'a', 't', 'a', 0x02, 0x00, 0x00, 0x00, 0x00, 0x00,
}
