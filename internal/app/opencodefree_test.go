package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/tests/mocknvidia"
)

// newOpenCodeFreeTestApp builds an app whose chat route can send OpenCodeFree
// models to the given gateway. gatewayURL may be empty to simulate an
// unconfigured gateway.
func newOpenCodeFreeTestApp(t *testing.T, gatewayURL string) (*App, string) {
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
	seedNVIDIAKeys(t, db, keySet, []string{"ocf-upstream-key"})

	nvidiaURL, err := url.Parse(mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"id":"nv-1","choices":[{"message":{"role":"assistant","content":"nvidia"}}]}`}).URL())
	if err != nil {
		t.Fatalf("parse nvidia url: %v", err)
	}
	cfg := config.Config{
		InitialAdminPassword: testInitialAdminPassword,
		DataDir:              t.TempDir(),
		MasterKey:            masterKey,
		NVIDIABaseURL:        nvidiaURL,
	}
	if gatewayURL != "" {
		ocfURL, err := url.Parse(gatewayURL)
		if err != nil {
			t.Fatalf("parse opencodefree url: %v", err)
		}
		cfg.OpenCodeFreeBaseURL = ocfURL
	}
	application, err := New(context.Background(), Dependencies{
		Config: cfg, DB: db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
		NVIDIAHTTPClient: http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	completeInitialPasswordChange(t, db)
	appOwnsDB = true
	t.Cleanup(func() { _ = application.Close() })
	return application, createdAccessKey.Plaintext
}

func seedChatModelWithProvider(t *testing.T, application *App, publicID, upstreamID, provider string) {
	t.Helper()
	repo := modelcatalog.NewRepository(application.db)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if _, err := repo.SaveSelectionsResult(context.Background(), []modelcatalog.Selection{{
		PublicID: publicID, UpstreamID: upstreamID, DisplayName: "Free Model",
		Kind: modelcatalog.KindChat, Enabled: true, SupportsReasoning: true, ReasoningWireFormat: "openai",
	}}, now); err != nil {
		t.Fatalf("save opencodefree model: %v", err)
	}
	model, err := repo.ResolveEnabled(context.Background(), publicID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	providerCopy := provider
	if _, _, err := repo.Patch(context.Background(), model.ID, modelcatalog.Patch{Provider: &providerCopy}, now); err != nil {
		t.Fatalf("patch provider: %v", err)
	}
}

func TestOpenCodeFreeChatRoutesToGatewayAndListsInModels(t *testing.T) {
	var hits int64
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"ocf-1","object":"chat.completion","model":"free-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello from opencodefree"},"finish_reason":"stop"}]}`)
	}))
	defer gateway.Close()

	application, accessToken := newOpenCodeFreeTestApp(t, gateway.URL)
	seedChatModelWithProvider(t, application, "pub-free", "free-model", modelcatalog.ProviderOpenCodeFree)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	// /v1/models must expose the OpenCodeFree model now that it is routable.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get models: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pub-free") {
		t.Fatalf("models missing opencodefree: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Non-stream chat routes to the gateway.
	reqBody := `{"model":"pub-free","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "hello from opencodefree") {
		t.Fatalf("chat did not come from gateway: %s", string(body))
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("gateway hits = %d, want 1", atomic.LoadInt64(&hits))
	}

	// Responses endpoint does not support the OpenCodeFree provider.
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"pub-free","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("responses: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("responses status = %d, want 501", resp.StatusCode)
	}
}

func TestOpenCodeFreeChatStreamRoutesToGateway(t *testing.T) {
	var hits int64
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"id":"ocf-s","choices":[{"delta":{"role":"assistant","content":"part one"}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, `data: {"id":"ocf-s","choices":[{"delta":{"content":" part two"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer gateway.Close()

	application, accessToken := newOpenCodeFreeTestApp(t, gateway.URL)
	seedChatModelWithProvider(t, application, "pub-free", "free-model", modelcatalog.ProviderOpenCodeFree)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	reqBody := `{"model":"pub-free","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":32}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream chat: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "part one") || !strings.Contains(string(body), "part two") || !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("stream body missing chunks: %s", string(body))
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("gateway hits = %d, want 1", atomic.LoadInt64(&hits))
	}
}

func TestOpenCodeFreeUnconfiguredReturns503(t *testing.T) {
	// No gateway configured: routing an OpenCodeFree model must be a clear 503,
	// not an internal error or a hang.
	application, accessToken := newOpenCodeFreeTestApp(t, "")
	seedChatModelWithProvider(t, application, "pub-free", "free-model", modelcatalog.ProviderOpenCodeFree)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"pub-free","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
