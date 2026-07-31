package app

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"mime/multipart"
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

func TestAudioAppProxiesValidatedJSON(t *testing.T) {
	want := `{"text":"hello"}`
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: want})
	t.Cleanup(upstream.Close)
	application, accessToken := newAudioTestApp(t, upstream, []string{"upstream-key-1"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	body, contentType := audioBody("public-asr", []byte{0x01, 0x02, 0x03})
	status, bodyRes := postAudio(t, server.URL, accessToken, body, contentType)
	if status != http.StatusOK || bodyRes != want {
		t.Fatalf("response = %d %s", status, bodyRes)
	}
	requests := upstream.Requests()
	if len(requests) != 1 || requests[0].Path != "/v1/audio/transcriptions" {
		t.Fatalf("upstream requests = %#v", requests)
	}
	if got := requests[0].Header.Get("Authorization"); got != "Bearer upstream-key-1" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAudioAppRetriesServiceUnavailableBeforeFirstByte(t *testing.T) {
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"temporary"}}`},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"text":"ok"}`},
	)
	t.Cleanup(upstream.Close)
	application, accessToken := newAudioTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	body, contentType := audioBody("public-asr", []byte{0x01, 0x02, 0x03})
	status, bodyRes := postAudio(t, server.URL, accessToken, body, contentType)
	if status != http.StatusOK || bodyRes != `{"text":"ok"}` {
		t.Fatalf("response = %d %s", status, bodyRes)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "upstream-key-1", "upstream-key-2")
}

func TestAudioAppRetriesMalformedSuccessBeforeCommit(t *testing.T) {
	// Non-streaming Audio: a 2xx with a malformed body (no text/transcript) is
	// retryable because the handler has not yet committed the response. The
	// Attempt loop tries the next key, mirroring the Chat handler behavior.
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, Body: `{"usage":{}}`},
		mocknvidia.Script{Status: http.StatusOK, Body: `{"text":"recovered"}`},
	)
	t.Cleanup(upstream.Close)
	application, accessToken := newAudioTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	body, contentType := audioBody("public-asr", []byte{0x01})
	status, bodyRes := postAudio(t, server.URL, accessToken, body, contentType)
	if status != http.StatusOK || bodyRes != `{"text":"recovered"}` {
		t.Fatalf("response = %d %s", status, bodyRes)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "upstream-key-1", "upstream-key-2")
}

func newAudioTestApp(t *testing.T, upstream *mocknvidia.Server, upstreamSecrets []string) (*App, string) {
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
	seedAudioModel(t, db)
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
	completeInitialPasswordChange(t, db)
	appOwnsDB = true
	t.Cleanup(func() { _ = application.Close() })
	return application, createdAccessKey.Plaintext
}

func seedAudioModel(t *testing.T, db *sql.DB) {
	t.Helper()
	// ASR/TTS require capability_verified_at to be enabled; set it here.
	verifiedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{
		{
			PublicID: "public-asr", UpstreamID: "vendor/asr", DisplayName: "Test ASR",
			Kind: modelcatalog.KindASR, Enabled: true, ReasoningWireFormat: "none",
			CapabilityVerifiedAt: timePtr(verifiedAt),
		},
		{
			PublicID: "public-tts", UpstreamID: "vendor/tts", DisplayName: "Test TTS",
			Kind: modelcatalog.KindTTS, Enabled: true, ReasoningWireFormat: "none",
			CapabilityVerifiedAt: timePtr(verifiedAt),
		},
	}, verifiedAt)
	if err != nil {
		t.Fatalf("save audio model: %v", err)
	}
}

func timePtr(v time.Time) *time.Time { return &v }

func postAudio(t *testing.T, baseURL, accessToken, body, contentType string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/audio/transcriptions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create audio request: %v", err)
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	request.Header.Set("Content-Type", contentType)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send audio request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read audio response: %v", err)
	}
	return response.StatusCode, string(payload)
}

func audioBody(model string, fileBytes []byte) (string, string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", model)
	part, _ := writer.CreateFormFile("file", "audio.wav")
	_, _ = part.Write(fileBytes)
	_ = writer.Close()
	return buf.String(), writer.FormDataContentType()
}

func TestSpeechAppProxiesBinaryAudio(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Content-Type", "audio/mpeg")
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: string([]byte{1, 2, 3}), Headers: headers})
	t.Cleanup(upstream.Close)
	application, accessToken := newAudioTestApp(t, upstream, []string{"upstream-key-1"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, contentType, body := postSpeech(t, server.URL, accessToken)
	if status != http.StatusOK || contentType != "audio/mpeg" || !bytes.Equal(body, []byte{1, 2, 3}) {
		t.Fatalf("response = %d %q %#v", status, contentType, body)
	}
	requests := upstream.Requests()
	if len(requests) != 1 || requests[0].Path != "/v1/audio/speech" {
		t.Fatalf("upstream requests = %#v", requests)
	}
}

func TestSpeechAppRetriesEmptySuccessBeforeFirstByte(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Content-Type", "audio/wav")
	upstream := mocknvidia.New(
		mocknvidia.Script{Status: http.StatusOK, Headers: headers},
		mocknvidia.Script{Status: http.StatusOK, Body: "audio", Headers: headers},
	)
	t.Cleanup(upstream.Close)
	application, accessToken := newAudioTestApp(t, upstream, []string{"upstream-key-1", "upstream-key-2"})
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, contentType, body := postSpeech(t, server.URL, accessToken)
	if status != http.StatusOK || contentType != "audio/wav" || string(body) != "audio" {
		t.Fatalf("response = %d %q %q", status, contentType, body)
	}
	assertAuthorizationOrder(t, upstream.Requests(), "upstream-key-1", "upstream-key-2")
}

func postSpeech(t *testing.T, baseURL, accessToken string) (int, string, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/audio/speech", strings.NewReader(`{
		"model":"public-tts","input":"sensitive input","voice":"alloy","response_format":"mp3"
	}`))
	if err != nil {
		t.Fatalf("create speech request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send speech request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read speech response: %v", err)
	}
	return response.StatusCode, response.Header.Get("Content-Type"), body
}
