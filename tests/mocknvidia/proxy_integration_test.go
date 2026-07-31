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
		`{"username":"admin","password":"admin"}`,
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
// key import (whose credential validation calls /v1/models) while the App is
// configured with XKProxyAPIURL. Every request must reach the TLS NVIDIA
// upstream through the local CONNECT proxy, sharing a single fetched lease.
//
// Note: streaming (Chat stream / Responses stream) is covered separately by
// TestProxyStreamingRequests, which currently documents a production bug in the
// proxy path (see there); non-streaming endpoints must all be 2xx here.
func TestAllEndpointsRouteThroughProxy(t *testing.T) {
	fixture := newProxyFixture(t)
	fixture.setResponses([]string{fixture.proxyAddress()})
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:      mocknvidia.New(),
		secrets:       []string{"nvapi-proxy-e2e-000001"},
		tlsUpstream:   fixture.upstream,
		proxyFetchURL: fixture.fetchURL(t),
		prepare: func(t *testing.T, db *sql.DB, keyIDs []int64, modelID int64) {
			seedProxySupportModels(t, db)
		},
	})

	// Key import → NVIDIA /v1/models credential validation through the proxy.
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

	if got := fixture.fetchCount(); got != 1 {
		t.Fatalf("proxy fetch calls = %d, want 1 (single lease reused across endpoints)", got)
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
		"/v1/models":               1,
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
// The request must route through the single proxy lease and the streamed body
// must be fully readable: the attempt context is never canceled after the
// response headers arrive, so the SSE body survives until the client stream
// completes. sseDelay forces the "headers received, first token pending"
// window so the test is deterministic instead of racing on warm connections.
func TestProxyStreamingRequests(t *testing.T) {
	fixture := newProxyFixture(t)
	fixture.setResponses([]string{fixture.proxyAddress()})
	fixture.upstream.sseDelay = 200 * time.Millisecond
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:      mocknvidia.New(),
		secrets:       []string{"nvapi-proxy-stream-0001"},
		tlsUpstream:   fixture.upstream,
		proxyFetchURL: fixture.fetchURL(t),
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
	if got := fixture.fetchCount(); got != 1 {
		t.Fatalf("proxy fetch calls = %d, want 1 (lease reused)", got)
	}
}

// TestProxyFetchFailureReturns502WithoutDirectTraffic proves a failed proxy
// fetch (non-IP body) surfaces as a stable 502 upstream_proxy_unavailable,
// never falls back to a direct connection, and leaves every NVIDIA key state
// field and model block untouched.
func TestProxyFetchFailureReturns502WithoutDirectTraffic(t *testing.T) {
	fixture := newProxyFixture(t)
	// No responses configured → the fetch API returns "not-an-ip-address".
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:      mocknvidia.New(),
		secrets:       []string{"nvapi-proxy-fetchfail-0001"},
		tlsUpstream:   fixture.upstream,
		proxyFetchURL: fixture.fetchURL(t),
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
	if got := fixture.fetchCount(); got < 1 {
		t.Fatalf("proxy fetch calls = %d, want >= 1", got)
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
// replay: the first fetched address is unreachable, the manager retires it,
// fetches a new address and replays the request with the same NVIDIA key. The
// TLS upstream must observe exactly one request carrying the same key.
func TestProxyFirstBrokenThenHealthyRetriesOnceSameKey(t *testing.T) {
	fixture := newProxyFixture(t)
	fixture.setResponses([]string{"127.0.0.1:1", fixture.proxyAddress()})
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:      mocknvidia.New(),
		secrets:       []string{"nvapi-proxy-retry-000001"},
		tlsUpstream:   fixture.upstream,
		proxyFetchURL: fixture.fetchURL(t),
	})

	status, body, _ := harness.request(t, http.MethodPost, "/v1/chat/completions", proxyChatBody())
	if status != http.StatusOK || !strings.Contains(body, "proxied-ok") {
		t.Fatalf("response = %d %s", status, body)
	}
	if got := fixture.fetchCount(); got != 2 {
		t.Fatalf("proxy fetch calls = %d, want 2 (one broken lease, one replay)", got)
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

// TestProxyErrorNoSecretLeakageInHTTPAndLogs injects a failing proxy fetch
// whose URL carries a fake apikey/sign/proxy address and a fake NVIDIA key, and
// asserts none of the secrets leak into the HTTP response or the application
// logs (mirroring the leak_test.go pattern).
func TestProxyErrorNoSecretLeakageInHTTPAndLogs(t *testing.T) {
	const (
		fakeApikey    = "fake-apikey-7f3c9d2e"
		fakeSign      = "fake-sign-2a8e4b6c"
		fakeProxyAddr = "198.51.100.7:8080"
		fakeNVIDIAKey = "nvapi-fake-key-leak-4c1d9e7f"
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previousDefault := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previousDefault) })

	fixture := newProxyFixture(t)
	// fetch URL carries the full fake credential surface; the fetch API returns
	// a non-IP body so the whole proxy chain fails.
	fetchRaw := fixture.fetchServer.URL + "?qty=1&apikey=" + fakeApikey + "&sign=" + fakeSign + "&ip=" + fakeProxyAddr
	fetchURL, err := url.Parse(fetchRaw)
	if err != nil {
		t.Fatalf("parse fake fetch URL: %v", err)
	}
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream:      mocknvidia.New(),
		secrets:       []string{fakeNVIDIAKey},
		tlsUpstream:   fixture.upstream,
		proxyFetchURL: fetchURL,
		logger:        logger,
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
			"fake apikey":   fakeApikey,
			"fake sign":     fakeSign,
			"fake API URL":  fetchRaw,
			"fake proxy IP": fakeProxyAddr,
			"NVIDIA key":    fakeNVIDIAKey,
			"Bearer value":  "Bearer " + fakeNVIDIAKey,
			"fetch query":   "apikey=" + fakeApikey,
			"qty parameter": "qty=1",
		} {
			if bytes.Contains(artifact, []byte(secret)) {
				t.Errorf("%s leaked %s %q", artifactName, label, printableFixture(secret))
			}
		}
	}
}
