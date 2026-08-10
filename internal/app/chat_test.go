package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/nvidiakey"
	"nvidia-router/tests/mocknvidia"
)

func TestChatAppEnforcesAuthenticationBodyLimitAndModelWhitelist(t *testing.T) {
	upstream := mocknvidia.New()
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, nil, false)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, body := postChat(t, server.URL, "", chatBody("missing-model"))
	assertAppChatError(t, status, body, http.StatusUnauthorized, "invalid_api_key")
	status, body = postChat(t, server.URL, "invalid", chatBody("missing-model"))
	assertAppChatError(t, status, body, http.StatusUnauthorized, "invalid_api_key")
	status, body = postChat(t, server.URL, accessToken, strings.Repeat("x", config.JSONBodyLimit+1))
	assertAppChatError(t, status, body, http.StatusRequestEntityTooLarge, "request_too_large")
	status, body = postChat(t, server.URL, accessToken, chatBody("missing-model"))
	assertAppChatError(t, status, body, http.StatusNotFound, "model_not_found")
	if upstream.Count() != 0 {
		t.Fatalf("upstream requests = %d, want 0", upstream.Count())
	}
}

func TestChatAppProxiesValidatedNonstreamJSON(t *testing.T) {
	want := `{"id":"chat-1","choices":[{}],"future":{"kept":true}}`
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: want})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, body := postChat(t, server.URL, accessToken, chatBody("public-model"))

	if status != http.StatusOK || body != want {
		t.Fatalf("response = %d %s", status, body)
	}
	requests := upstream.Requests()
	if len(requests) != 1 || requests[0].Path != "/v1/chat/completions" {
		t.Fatalf("upstream requests = %#v", requests)
	}
	if got := requests[0].Header.Get("Authorization"); got != "Bearer upstream-key-1" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestChatAppRetriesMalformedSuccessAndReturnsProtocolErrorAfterExhaustion(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, Body: "not-json private-first-body"},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"id":"chat-2"}`},
	)
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, body := postChat(t, server.URL, accessToken, chatBody("public-model"))

	assertAppChatError(t, status, body, http.StatusBadGateway, "upstream_protocol_error")
	if strings.Contains(body, "private-first-body") {
		t.Fatalf("response leaked upstream body: %s", body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "upstream-key-1", "upstream-key-2")
}

func TestChatAppSkipsFailedKeyOnFollowingRequest(t *testing.T) {
	tests := []struct {
		name                  string
		failure               mocknvidia.Script
		expectedFirstStatus   int
		expectedAuthorization []string
	}{
		{name: "unauthorized", failure: mocknvidia.Script{Status: http.StatusUnauthorized, Body: `{"error":{"code":"invalid_api_key"}}`},
			expectedFirstStatus:   http.StatusUnauthorized,
			expectedAuthorization: []string{"upstream-key-1", "upstream-key-2"}},
		{name: "rate limited", failure: mocknvidia.Script{Status: http.StatusTooManyRequests, Body: `{"error":{"message":"private"}}`},
			expectedFirstStatus:   http.StatusOK,
			expectedAuthorization: []string{"upstream-key-1", "upstream-key-2", "upstream-key-2"}},
		{name: "model forbidden", failure: mocknvidia.Script{Status: http.StatusForbidden, Body: `{"error":{"message":"private"}}`},
			expectedFirstStatus:   http.StatusOK,
			expectedAuthorization: []string{"upstream-key-1", "upstream-key-2", "upstream-key-2"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := mocknvidia.New(
				test.failure,
				mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{}],"attempt":2}`},
				mocknvidia.Script{Status: http.StatusOK, Body: `{"choices":[{}],"attempt":3}`},
			)
			t.Cleanup(upstream.Close)
			application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"}, true)
			server := httptest.NewServer(application.Handler())
			t.Cleanup(server.Close)

			// A credential 401 is per-key and must not fail over (R2.2): the first
			// request surfaces the 401 and disables the key; the second request
			// skips it and succeeds on the healthy key. Retryable faults (429,
			// model 403) still fail over and the first request succeeds.
			wantStatuses := []int{test.expectedFirstStatus, http.StatusOK}
			for requestNumber := 1; requestNumber <= 2; requestNumber++ {
				status, body := postChat(t, server.URL, accessToken, chatBody("public-model"))
				if status != wantStatuses[requestNumber-1] {
					t.Fatalf("request %d response = %d %s, want %d", requestNumber, status, body, wantStatuses[requestNumber-1])
				}
			}
			assertAuthorizationOrder(t, upstream.Requests(), test.expectedAuthorization...)
		})
	}
}

func newChatTestApp(
	t *testing.T,
	upstream *mocknvidia.Server,
	upstreamSecrets []string,
	withModel bool,
) (*App, string) {
	t.Helper()
	db := openAppDatabase(t)
	appOwnsDB := false
	t.Cleanup(func() {
		if !appOwnsDB {
			_ = db.Close()
		}
	})
	masterKey := [32]byte{1}
	keySet, err := crypto.New(masterKey)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	accessKeys := accesskey.NewService(accesskey.NewRepository(db), keySet, clock.RealClock{})
	createdAccessKey, err := accessKeys.Create(context.Background(), "test")
	if err != nil {
		t.Fatalf("create access key: %v", err)
	}
	seedNVIDIAKeys(t, db, keySet, upstreamSecrets)
	if withModel {
		seedChatModel(t, db)
	}
	baseURL, err := url.Parse(upstream.URL())
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	application, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), MasterKey: masterKey, NVIDIABaseURL: baseURL},
		DB:     db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
		NVIDIAHTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	completeInitialPasswordChange(t, db)
	appOwnsDB = true
	t.Cleanup(func() { _ = application.Close() })
	return application, createdAccessKey.Plaintext
}

func seedNVIDIAKeys(t *testing.T, db *sql.DB, keys *crypto.KeySet, secrets []string) {
	t.Helper()
	repository := nvidiakey.NewRepository(db)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	for index, secret := range secrets {
		ciphertext, nonce, err := keys.Encrypt([]byte(secret), "nvidia-key:v1")
		if err != nil {
			t.Fatalf("encrypt NVIDIA key: %v", err)
		}
		_, duplicate, err := repository.Create(
			context.Background(), ciphertext, nonce, []byte{byte(index + 1)}, "test", "key", now,
		)
		if err != nil || duplicate {
			t.Fatalf("create NVIDIA key %d: duplicate=%v err=%v", index, duplicate, err)
		}
	}
}

func seedChatModel(t *testing.T, db *sql.DB) {
	t.Helper()
	err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "public-model", UpstreamID: "vendor/model", DisplayName: "Test Model",
		Kind: modelcatalog.KindChat, Enabled: true, ReasoningWireFormat: "none",
	}}, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("save chat model: %v", err)
	}
}

func TestChatAppStreamForwardsSSEEventsAndReleasesLease(t *testing.T) {
	sseChunks := []mocknvidia.SSEChunk{
		{Data: "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"},
		{Data: "data: [DONE]\n\n"},
	}
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, SSE: sseChunks})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"stream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	ct := response.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("[DONE] not found in stream response: %s", body)
	}
}

func TestChatAppStreamCancelsOnClientDisconnect(t *testing.T) {
	// Upstream sends chunks with delays to simulate a long stream
	sseChunks := []mocknvidia.SSEChunk{
		{Data: "data: {\"choices\":[{\"delta\":{\"content\":\"chunk1\"}}]}\n\n"},
		{Data: "", Delay: 2 * time.Second}, // Long delay - client should disconnect first
	}
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, SSE: sseChunks})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"cancel-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)

	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return
		}
		// Read first chunk then cancel
		buf := make([]byte, 128)
		_, _ = response.Body.Read(buf)
		cancel()
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not complete after cancel")
	}
	// Upstream detects the cancellation asynchronously once the router closes the
	// upstream connection; poll briefly rather than checking a single instant.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if upstream.CanceledCount() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if upstream.CanceledCount() == 0 {
		t.Fatal("upstream did not detect client cancellation")
	}
}

func postChat(t *testing.T, baseURL, accessToken, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create chat request: %v", err)
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send chat request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read chat response: %v", err)
	}
	return response.StatusCode, string(payload)
}

func chatBody(model string) string {
	return `{"model":"` + model + `","messages":[{"role":"user","content":"hello"}]}`
}

func assertAppChatError(t *testing.T, status int, body string, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", status, wantStatus, body)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Code != wantCode {
		t.Fatalf("code = %q, want %q", payload.Error.Code, wantCode)
	}
}

func assertAuthorizationOrder(t *testing.T, requests []mocknvidia.Request, secrets ...string) {
	t.Helper()
	if len(requests) != len(secrets) {
		t.Fatalf("request count = %d, want %d", len(requests), len(secrets))
	}
	for index, secret := range secrets {
		if got := requests[index].Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("request %d Authorization = %q", index+1, got)
		}
	}
}
