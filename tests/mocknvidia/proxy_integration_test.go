package mocknvidia_test

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/tests/mocknvidia"
)

const (
	publicEmbeddingModel   = "public-embed"
	publicASRModel         = "public-asr"
	publicTTSModel         = "public-tts"
	upstreamEmbeddingModel = "vendor/embed"
	upstreamASRModel       = "vendor/asr"
	upstreamTTSModel       = "vendor/tts"
)

// seedProxySupportModels adds the embedding / ASR / TTS models used by the
// proxy endpoint tests. ASR and TTS require a verified capability timestamp.
func seedProxySupportModels(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	verified := now
	err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{
		{PublicID: publicEmbeddingModel, UpstreamID: upstreamEmbeddingModel, DisplayName: "Proxy Embedding", Kind: modelcatalog.KindEmbedding, Enabled: true, ReasoningWireFormat: "none"},
		{PublicID: publicASRModel, UpstreamID: upstreamASRModel, DisplayName: "Proxy ASR", Kind: modelcatalog.KindASR, Enabled: true, ReasoningWireFormat: "none", CapabilityVerifiedAt: &verified},
		{PublicID: publicTTSModel, UpstreamID: upstreamTTSModel, DisplayName: "Proxy TTS", Kind: modelcatalog.KindTTS, Enabled: true, ReasoningWireFormat: "none", CapabilityVerifiedAt: &verified},
	}, now)
	if err != nil {
		t.Fatalf("seed proxy support models: %v", err)
	}
}

// adminSessionCookie logs in with the default credentials (the harness clears
// must_change_password before the harness is returned) and returns the session
// cookie so admin management endpoints can be exercised end to end.
func adminSessionCookie(t *testing.T, harness *appHarness) string {
	t.Helper()
	result := harness.doRequest(context.Background(), http.MethodPost, "/admin/api/auth/login",
		`{"username":"admin","password":"test-initial-admin-password"}`,
		http.Header{"Origin": []string{harness.server.URL}})
	if result.err != nil || result.status != http.StatusOK {
		t.Fatalf("admin login = %d %s err=%v", result.status, result.body, result.err)
	}
	for _, value := range result.header["Set-Cookie"] {
		if strings.HasPrefix(value, "nvr_admin_session=") {
			if i := strings.Index(value, ";"); i >= 0 {
				return value[:i]
			}
			return value
		}
	}
	t.Fatalf("admin login did not set a session cookie: headers=%v", result.header)
	return ""
}

type nvidiaKeyState struct {
	authInvalid         int
	cooldownUntil       sql.NullString
	consecutiveFailures int
	lastErrorCode       sql.NullString
}

func readKeyState(t *testing.T, db *sql.DB, keyID int64) nvidiaKeyState {
	t.Helper()
	var state nvidiaKeyState
	err := db.QueryRow(`SELECT auth_invalid, cooldown_until, consecutive_failures, last_error_code FROM nvidia_keys WHERE id = ?`, keyID).
		Scan(&state.authInvalid, &state.cooldownUntil, &state.consecutiveFailures, &state.lastErrorCode)
	if err != nil {
		t.Fatalf("load NVIDIA key state: %v", err)
	}
	return state
}

func modelBlockCount(t *testing.T, db *sql.DB, keyID, modelID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nvidia_key_model_blocks WHERE nvidia_key_id = ? AND model_id = ?`, keyID, modelID).Scan(&count); err != nil {
		t.Fatalf("load model block count: %v", err)
	}
	return count
}

func proxyChatBody() string {
	return `{"model":"public-chat","messages":[{"role":"user","content":"hello"}]}`
}

// TestAllEndpointsRouteThroughProxy exercises every public v1 endpoint plus a
// local-only key import while the App is configured with the proxy-pool
// endpoint. Every request must reach the TLS NVIDIA upstream through the local
// CONNECT proxy.
//
// Note: streaming (Chat stream / Responses stream) is covered separately by
// TestProxyStreamingRequests, which currently documents a production bug in the
// proxy path (see there); non-streaming endpoints must all be 2xx here.
func TestAllEndpointsRouteThroughProxy(t *testing.T) {
	fixture := newProxyFixture(t)
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{"nvapi-proxy-e2e-000001"},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
		prepare: func(t *testing.T, db *sql.DB, keyIDs []int64, modelID int64) {
			seedProxySupportModels(t, db)
		},
	})

	// Key import is local-only under the delayed validation contract.
	cookie := adminSessionCookie(t, harness)
	importResult := harness.doRequest(context.Background(), http.MethodPost, "/admin/api/nvidia-keys",
		`{"key":"nvapi-import-key-1234567890"}`,
		http.Header{"Origin": []string{harness.server.URL}, "Cookie": []string{cookie}})
	if importResult.err != nil || importResult.status != http.StatusCreated {
		t.Fatalf("key import = %d %s err=%v", importResult.status, importResult.body, importResult.err)
	}
	if !strings.Contains(importResult.body, `"status":"imported"`) {
		t.Fatalf("key import did not report imported: %s", importResult.body)
	}

	// Chat non-streaming.
	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusOK || !strings.Contains(body, "proxied-ok") {
		t.Fatalf("chat non-stream = %d %s", status, body)
	}
	// Responses non-streaming (translated to chat upstream).
	status, body, _ = harness.request(t, http.MethodPost, "/v1/responses", `{
		"model":"public-chat","input":"hello"
	}`)
	if status != http.StatusOK || !strings.Contains(body, "proxied-ok") {
		t.Fatalf("responses non-stream = %d %s", status, body)
	}
	// Embeddings.
	status, body, _ = harness.request(t, http.MethodPost, "/v1/embeddings", `{
		"model":"public-embed","input":"hello"
	}`)
	if status != http.StatusOK || !strings.Contains(body, `"embedding"`) {
		t.Fatalf("embeddings = %d %s", status, body)
	}
	// Audio transcriptions (multipart).
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	if err := writer.WriteField("model", publicASRModel); err != nil {
		t.Fatalf("write ASR model field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "probe.wav")
	if err != nil {
		t.Fatalf("create ASR file part: %v", err)
	}
	if _, err := part.Write(probeWAVBytes); err != nil {
		t.Fatalf("write ASR audio bytes: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ASR multipart writer: %v", err)
	}
	asrResult := harness.doRequest(context.Background(), http.MethodPost, "/v1/audio/transcriptions",
		multipartBody.String(),
		http.Header{"Content-Type": []string{writer.FormDataContentType()}})
	if asrResult.err != nil || asrResult.status != http.StatusOK || !strings.Contains(asrResult.body, "proxied-transcript") {
		t.Fatalf("audio transcriptions = %d %s err=%v", asrResult.status, asrResult.body, asrResult.err)
	}
	// Audio speech.
	status, body, _ = harness.request(t, http.MethodPost, "/v1/audio/speech", `{
		"model":"public-tts","input":"hello","voice":"Magpie-Multilingual.EN-US.Aria","response_format":"wav"
	}`)
	if status != http.StatusOK || body != string(probeWAVBytes) {
		t.Fatalf("audio speech = %d len(body)=%d", status, len(body))
	}

	if got := fixture.connectCount(); got < 1 {
		t.Fatalf("proxy CONNECT count = %d, want >= 1", got)
	}
	requests := fixture.upstream.requestsSnapshot()
	paths := make(map[string]int)
	for _, request := range requests {
		paths[request.Path]++
	}
	expected := map[string]int{
		"/v1/chat/completions":     2,
		"/v1/embeddings":           1,
		"/v1/audio/transcriptions": 1,
		"/v1/audio/speech":         1,
	}
	for path, want := range expected {
		if got := paths[path]; got != want {
			t.Fatalf("TLS upstream %s request count = %d, want %d (all=%v)", path, got, want, paths)
		}
	}
}

// TestProxyStreamingRequests covers streaming (SSE) through the proxy path.
// The request must route through the shared proxy-pool transport and the streamed body
// must be fully readable: the attempt context is never canceled after the
// response headers arrive, so the SSE body survives until the client stream
// completes. sseDelay forces the "headers received, first token pending"
// window so the test is deterministic instead of racing on warm connections.
func TestProxyStreamingRequests(t *testing.T) {
	fixture := newProxyFixture(t)
	fixture.upstream.sseDelay = 200 * time.Millisecond
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{"nvapi-proxy-stream-0001"},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
	})

	for _, tc := range []struct {
		name   string
		path   string
		body   string
		wantIn string
	}{
		{name: "chat_stream", path: "/v1/chat/completions", body: `{
			"model":"public-chat","messages":[{"role":"user","content":"hello"}],"stream":true
		}`, wantIn: "proxied-stream"},
		{name: "responses_stream", path: "/v1/responses", body: `{
			"model":"public-chat","input":"hello","stream":true
		}`, wantIn: "event: response.completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := fixture.upstream.count()
			status, body, _ := harness.request(t, http.MethodPost, tc.path, tc.body)
			if fixture.upstream.count() != before+1 {
				t.Fatalf("TLS upstream did not receive the streaming request: before=%d after=%d", before, fixture.upstream.count())
			}
			if status != http.StatusOK {
				t.Fatalf("streaming response = %d %s, want 200", status, body)
			}
			if !strings.Contains(body, tc.wantIn) {
				t.Fatalf("streaming body missing %q:\n%s", tc.wantIn, body)
			}
		})
	}
}

// TestProxyUnavailableReturns502WithoutDirectTraffic proves an unavailable
// proxy-pool endpoint surfaces as a stable 502 and never falls back to a direct
// connection, while leaving NVIDIA key state untouched.
func TestProxyUnavailableReturns502WithoutDirectTraffic(t *testing.T) {
	fixture := newProxyFixture(t)
	proxyURL, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("parse unavailable proxy URL: %v", err)
	}
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:     mocknvidia.New(),
		secrets:      []string{"nvapi-proxy-unavailable-0001"},
		tlsUpstream:  fixture.upstream,
		proxyURL:     proxyURL,
		proxyAuthKey: "proxy-secret",
		settings: runtimeconfig.Snapshot{
			QueueCapacity: 10, QueueWaitTimeoutMS: 1000,
			ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000,
			NonstreamTotalTimeoutMS: 5000, ShutdownGraceMS: 1000,
		},
	})
	keyID := harness.keyIDs[0]
	modelID := harness.modelID
	before := readKeyState(t, harness.db, keyID)
	beforeBlocks := modelBlockCount(t, harness.db, keyID, modelID)

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusBadGateway {
		t.Fatalf("response = %d %s", status, body)
	}
	if !strings.Contains(body, `"code":"upstream_proxy_unavailable"`) || !strings.Contains(body, "The upstream proxy is temporarily unavailable.") {
		t.Fatalf("response body missing proxy unavailable code/message: %s", body)
	}
	if got := fixture.upstream.count(); got != 0 {
		t.Fatalf("TLS upstream received %d direct requests, want 0 (no direct fallback)", got)
	}
	after := readKeyState(t, harness.db, keyID)
	if after != before {
		t.Fatalf("NVIDIA key state changed after proxy failure: before=%+v after=%+v", before, after)
	}
	if got := modelBlockCount(t, harness.db, keyID, modelID); got != beforeBlocks {
		t.Fatalf("model block count changed after proxy failure: before=%d after=%d", beforeBlocks, got)
	}
}

// TestProxyFirstBrokenThenHealthyRetriesOnceSameKey proves the proxy-internal
// replay: the first CONNECT to the proxy pool fails, then the same endpoint
// succeeds and the request is replayed with the same NVIDIA key.
func TestProxyFirstBrokenThenHealthyRetriesOnceSameKey(t *testing.T) {
	fixture := newProxyFixture(t)
	fixture.failNextConnect()
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{"nvapi-proxy-retry-000001"},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusOK || !strings.Contains(body, "proxied-ok") {
		t.Fatalf("response = %d %s", status, body)
	}
	if got := fixture.connectCount(); got < 1 {
		t.Fatalf("proxy CONNECT count = %d, want >= 1", got)
	}
	requests := fixture.upstream.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("TLS upstream requests = %d, want 1 (replay happened only before any upstream byte)", len(requests))
	}
	if !strings.HasPrefix(requests[0].Authorization, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(requests[0].Authorization, "Bearer ")) == "" {
		t.Fatalf("TLS upstream Authorization = %q, want Bearer <same-key>", requests[0].Authorization)
	}
}

// TestProxy5xxConnectDoesNotRetrySameKey locks in the R2.2 tightening at the
// integration level: a 5xx CONNECT answer means the proxy is up and already
// refused the request, so the client must not replay — the request surfaces the
// proxy error and the healthy second CONNECT is never attempted.
func TestProxy5xxConnectDoesNotRetrySameKey(t *testing.T) {
	fixture := newProxyFixture(t)
	fixture.failNextConnectWithStatus(http.StatusServiceUnavailable)
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{"nvapi-proxy-5xx-no-replay"},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusBadGateway {
		t.Fatalf("response = %d %s, want 502 proxy error", status, body)
	}
	if got := fixture.connectCount(); got != 1 {
		t.Fatalf("proxy CONNECT count = %d, want 1 (5xx CONNECT must not be replayed)", got)
	}
	if got := fixture.upstream.count(); got != 0 {
		t.Fatalf("TLS upstream requests = %d, want 0", got)
	}
}

// TestProxyUpstream429RetriesNextKeyHonoringRetryAfter proves that in proxy
// mode an upstream 429 with Retry-After drives the status-based key switch: the
// throttled key is parked for the Retry-After window while the request retries
// on a different key. The upstream must see exactly one request per key — the
// client transport must not replay the 429 (a replay would re-send the same
// throttled key instead of moving on).
func TestProxyUpstream429RetriesNextKeyHonoringRetryAfter(t *testing.T) {
	firstSecret := "nvapi-proxy-429-000001"
	secondSecret := "nvapi-proxy-429-000002"
	fixture := newProxyFixture(t)
	fixture.upstream.throttleKey("Bearer "+firstSecret, http.StatusTooManyRequests, "2")
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{firstSecret, secondSecret},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
	})
	keyID := harness.keyIDs[0]

	start := time.Now()
	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusOK || !strings.Contains(body, "proxied-ok") {
		t.Fatalf("response = %d %s", status, body)
	}
	elapsed := time.Since(start)

	requests := fixture.upstream.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("TLS upstream requests = %d, want 2 (one per key; no transport replay)", len(requests))
	}
	if requests[0].Authorization != "Bearer "+firstSecret {
		t.Fatalf("request 1 Authorization = %q, want the throttled key first", requests[0].Authorization)
	}
	if requests[1].Authorization != "Bearer "+secondSecret {
		t.Fatalf("request 2 Authorization = %q, want the healthy key second", requests[1].Authorization)
	}
	state := readKeyState(t, harness.db, keyID)
	if state.authInvalid != 0 || state.consecutiveFailures != 1 || state.lastErrorCode.String != "rate_limit_exceeded" {
		t.Fatalf("429 key state = %+v, want one rate-limit failure, key still enabled", state)
	}
	// Retry-After honoured: the router slept the 2s hint before switching keys,
	// so the request could not have returned before the backoff, and the
	// throttled key's cooldown (anchored to the 429) is just about elapsed.
	if elapsed < 1500*time.Millisecond {
		t.Fatalf("request returned after %s, want the Retry-After backoff to have been honored", elapsed)
	}
	assertProxyRetryAfterElapsed(t, harness, keyID)
}

// TestProxyUpstream529RetriesNextKeyWithoutTransportReplay proves the
// NVIDIA-specific overload status 529 is treated as a retryable upstream fault
// in proxy mode too: the throttled key is failed over to a healthy key and the
// upstream sees exactly one request per key (an HTTP status answer is not a
// transport error, so the client transport must not replay it).
func TestProxyUpstream529RetriesNextKeyWithoutTransportReplay(t *testing.T) {
	firstSecret := "nvapi-proxy-529-000001"
	secondSecret := "nvapi-proxy-529-000002"
	fixture := newProxyFixture(t)
	fixture.upstream.throttleKey("Bearer "+firstSecret, 529, "")
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{firstSecret, secondSecret},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
	})
	keyID := harness.keyIDs[0]

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusOK || !strings.Contains(body, "proxied-ok") {
		t.Fatalf("response = %d %s", status, body)
	}
	requests := fixture.upstream.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("TLS upstream requests = %d, want 2 (one per key; no transport replay)", len(requests))
	}
	if requests[0].Authorization != "Bearer "+firstSecret {
		t.Fatalf("request 1 Authorization = %q, want the 529 key first", requests[0].Authorization)
	}
	if requests[1].Authorization != "Bearer "+secondSecret {
		t.Fatalf("request 2 Authorization = %q, want the healthy key second", requests[1].Authorization)
	}
	state := readKeyState(t, harness.db, keyID)
	if state.authInvalid != 0 || state.consecutiveFailures != 1 || state.lastErrorCode.String != "upstream_error" {
		t.Fatalf("529 key state = %+v, want one upstream failure, key still enabled", state)
	}
	if !state.cooldownUntil.Valid {
		t.Fatalf("529 key was not cooled down")
	}
}

// TestProxyDeepSeekV4FlashReasoningStream proves the full DeepSeek v4-flash
// long-task path through the proxy: the native `thinking` parameter is
// forwarded unchanged upstream, and the SSE
// reasoning_content relay reaches the client over the CONNECT tunnel.
func TestProxyDeepSeekV4FlashReasoningStream(t *testing.T) {
	fixture := newProxyFixture(t)
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:    mocknvidia.New(),
		secrets:     []string{"nvapi-proxy-ds-flash-1"},
		tlsUpstream: fixture.upstream,
		proxyURL:    fixture.proxyURL(t),
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
	if status != http.StatusOK || !strings.Contains(body, "proxied-reasoning") || !strings.Contains(body, "proxied-stream") {
		t.Fatalf("response = %d %s", status, body)
	}
	if got := fixture.connectCount(); got != 1 {
		t.Fatalf("proxy CONNECT count = %d, want 1", got)
	}
	requests := fixture.upstream.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("TLS upstream requests = %d, want 1", len(requests))
	}
	if !strings.Contains(requests[0].Body, `"thinking"`) {
		t.Fatalf("upstream body did not preserve native thinking: %s", requests[0].Body)
	}
}

// assertProxyRetryAfterElapsed asserts the throttled key's cooldown is just
// about now: the router slept the upstream Retry-After before switching keys, so
// the parked window has almost elapsed when the fallback response returns. A
// still-future cooldown means the Retry-After was ignored; a long-past one means
// it was never anchored to the 429.
func assertProxyRetryAfterElapsed(t *testing.T, harness *appHarness, keyID int64) {
	t.Helper()
	var raw string
	if err := harness.db.QueryRow(`SELECT cooldown_until FROM nvidia_keys WHERE id = ?`, keyID).Scan(&raw); err != nil {
		t.Fatalf("load cooldown: %v", err)
	}
	until, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse cooldown %q: %v", raw, err)
	}
	remaining := time.Until(until)
	if remaining > time.Second || -remaining > 3*time.Second {
		t.Fatalf("Retry-After cooldown remaining = %v, want just-expired after the Retry-After backoff", remaining)
	}
}

// TestProxyErrorNoSecretLeakageInHTTPAndLogs injects a failing proxy-pool
// endpoint and a fake proxy authentication key, then asserts none of the
// secrets leak into the HTTP response or application logs.
func TestProxyErrorNoSecretLeakageInHTTPAndLogs(t *testing.T) {
	const (
		fakeProxyAuthKey = "fake-proxy-auth-7f3c9d2e"
		fakeProxyURL     = "http://127.0.0.1:1"
		fakeNVIDIAKey    = "nvapi-fake-key-leak-4c3d9e7f"
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previousDefault := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previousDefault) })

	fixture := newProxyFixture(t)
	proxyURL, err := url.Parse(fakeProxyURL)
	if err != nil {
		t.Fatalf("parse fake proxy URL: %v", err)
	}
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:     mocknvidia.New(),
		secrets:      []string{fakeNVIDIAKey},
		tlsUpstream:  fixture.upstream,
		proxyURL:     proxyURL,
		proxyAuthKey: fakeProxyAuthKey,
		logger:       logger,
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusBadGateway || !strings.Contains(body, `"code":"upstream_proxy_unavailable"`) {
		t.Fatalf("response = %d %s", status, body)
	}
	if got := fixture.upstream.count(); got != 0 {
		t.Fatalf("TLS upstream received %d direct requests, want 0", got)
	}

	artifacts := map[string][]byte{
		"http_response": []byte(body),
		"slog":          logs.Bytes(),
	}
	for artifactName, artifact := range artifacts {
		for label, secret := range map[string]string{
			"proxy auth key": fakeProxyAuthKey,
			"proxy URL":      fakeProxyURL,
			"NVIDIA key":     fakeNVIDIAKey,
			"Bearer value":   "Bearer " + fakeNVIDIAKey,
		} {
			if bytes.Contains(artifact, []byte(secret)) {
				t.Errorf("%s leaked %s %q", artifactName, label, printableFixture(secret))
			}
		}
	}
}
