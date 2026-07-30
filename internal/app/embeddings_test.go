package app

import (
	"context"
	"database/sql"
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
	"nvidia-router/tests/mocknvidia"
)

func TestEmbeddingsAppProxiesValidatedJSON(t *testing.T) {
	want := `{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2}}`
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: want})
	t.Cleanup(upstream.Close)
	application, accessToken := newEmbeddingsTestApp(t, upstream, []string{"upstream-key-1"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, body := postEmbeddings(t, server.URL, accessToken, `{"model":"public-embed","input":"hi"}`)
	if status != http.StatusOK || body != want {
		t.Fatalf("response = %d %s", status, body)
	}
	requests := upstream.Requests()
	if len(requests) != 1 || requests[0].Path != "/v1/embeddings" {
		t.Fatalf("upstream requests = %#v", requests)
	}
	if got := requests[0].Header.Get("Authorization"); got != "Bearer upstream-key-1" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestEmbeddingsAppRetriesMalformedSuccessAndSurfacesProtocolError(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, Body: "not-json private-first-body"},
		mocknvidia.Script{Status: http.StatusOK, Body: "also-not-json private-second-body"},
	)
	t.Cleanup(upstream.Close)
	application, accessToken := newEmbeddingsTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, body := postEmbeddings(t, server.URL, accessToken, `{"model":"public-embed","input":"hi"}`)
	assertAppChatError(t, status, body, http.StatusBadGateway, "upstream_protocol_error")
	if strings.Contains(body, "private-first-body") || strings.Contains(body, "private-second-body") {
		t.Fatalf("response leaked upstream body: %s", body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "upstream-key-1", "upstream-key-2")
}

func TestEmbeddingsAppSkipsFailedKeyOnFollowingRequest(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusUnauthorized, Body: `{"error":{"code":"invalid_api_key"}}`},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"data":[{"embedding":[0.1]}]}`},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"data":[{"embedding":[0.2]}]}`},
	)
	t.Cleanup(upstream.Close)
	application, accessToken := newEmbeddingsTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	for requestNumber := 1; requestNumber <= 2; requestNumber++ {
		status, body := postEmbeddings(t, server.URL, accessToken, `{"model":"public-embed","input":"hi"}`)
		if status != http.StatusOK {
			t.Fatalf("request %d response = %d %s", requestNumber, status, body)
		}
	}
	assertAuthorizationOrder(t, upstream.Requests(), "upstream-key-1", "upstream-key-2", "upstream-key-2")
}

func newEmbeddingsTestApp(t *testing.T, upstream *mocknvidia.Server, upstreamSecrets []string) (*App, string) {
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
	seedEmbeddingModel(t, db)
	baseURL, err := url.Parse(upstream.URL())
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	application, err := New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), MasterKey: masterKey, NVIDIABaseURL: baseURL},
		DB:     db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
		NVIDIAHTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	appOwnsDB = true
	t.Cleanup(func() { _ = application.Close() })
	return application, createdAccessKey.Plaintext
}

func seedEmbeddingModel(t *testing.T, db *sql.DB) {
	t.Helper()
	err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "public-embed", UpstreamID: "vendor/embed", DisplayName: "Test Embedding",
		Kind: modelcatalog.KindEmbedding, Enabled: true, ReasoningWireFormat: "none",
	}}, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("save embedding model: %v", err)
	}
}

func postEmbeddings(t *testing.T, baseURL, accessToken, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/embeddings", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create embeddings request: %v", err)
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send embeddings request: %v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read embeddings response: %v", err)
	}
	return response.StatusCode, string(payload)
}
