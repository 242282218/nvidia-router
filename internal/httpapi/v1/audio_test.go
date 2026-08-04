package v1

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
	"nvidia-router/internal/upstream/nvidia"
)

func TestAudioRejectsNonMultipart(t *testing.T) {
	handler := NewAudio(nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("x"))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusBadRequest, "invalid_audio")
}

func TestAudioRejectsMissingFile(t *testing.T) {
	handler := NewAudio(nil, nil, nil)
	body, contentType := multipartBodyAudio("", nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusBadRequest, "missing_required_parameter")
}

func TestAudioRejectsWhenBodyReadAdmissionIsSaturated(t *testing.T) {
	body, contentType := multipartBodyAudio("public-asr", map[string][]byte{"file": {1}})
	for range cap(bodyReadSemaphore) {
		bodyReadSemaphore <- struct{}{}
	}
	defer func() {
		for range cap(bodyReadSemaphore) {
			<-bodyReadSemaphore
		}
	}()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	NewAudio(nil, nil, nil).ServeHTTP(response, request)
	assertChatError(t, response, http.StatusTooManyRequests, "server_busy")
}

// TestAudioSlowUploadTimesOut verifies that a stalled multipart upload cannot
// hold a body-read slot forever: the read deadline must close the wire body
// (http.Server has no ReadTimeout because SSE streams stay open) and the
// handler must surface 408 request_timeout.
func TestAudioSlowUploadTimesOut(t *testing.T) {
	body := &stallingUploadBody{unblocked: make(chan struct{})}
	handler := NewAudio(nil, nil, nil)
	handler.bodyReadTimeout = 40 * time.Millisecond
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=slow")
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusRequestTimeout, "request_timeout")
	if !body.closed.Load() {
		t.Fatal("slow upload body was not closed when the read deadline fired")
	}
	// The read slot must be returned even on timeout so a flood of stalled
	// uploads cannot starve every other request. The channel holds one element
	// per occupied slot, so zero elements means all slots are free again.
	if held := len(bodyReadSemaphore); held != 0 {
		t.Fatalf("body read slots held after timeout: %d/%d", held, cap(bodyReadSemaphore))
	}
}

// stallingUploadBody yields a single byte then blocks until closed,
// simulating a client that accepts the connection but never finishes the
// upload. Close unblocks Read so the handler can actually return.
type stallingUploadBody struct {
	emitted   atomic.Bool
	closed    atomic.Bool
	unblocked chan struct{}
	closeOnce sync.Once
}

func (b *stallingUploadBody) Read(payload []byte) (int, error) {
	if b.emitted.CompareAndSwap(false, true) {
		if len(payload) > 0 {
			payload[0] = 'x'
			return 1, nil
		}
	}
	<-b.unblocked
	return 0, io.EOF
}

func (b *stallingUploadBody) Close() error {
	b.closed.Store(true)
	b.closeOnce.Do(func() { close(b.unblocked) })
	return nil
}

func TestAudioRejectsNonASRModelKind(t *testing.T) {
	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelKindMismatch
	})
	handler := NewAudio(resolver, nil, nil)
	body, contentType := multipartBodyAudio("public-asr", map[string][]byte{"file": {1, 2, 3}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusNotImplemented, "not_implemented")
}

func TestAudioMapsModelAndPreservesValidatedResponse(t *testing.T) {
	type capturedRequest struct {
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 1)
	responseBody := []byte(`{"text":"hello"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured <- capturedRequest{header: request.Header.Clone(), body: body}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(responseBody)
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.ASR.URL = upstream.URL + "/v1/audio/transcriptions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 11}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
		if publicID != "public-asr" || requirements.Kind != modelcatalog.KindASR {
			t.Fatalf("resolve = %q, %#v", publicID, requirements)
		}
		return modelcatalog.Model{
			ID: 31, PublicID: publicID, UpstreamID: "vendor/asr", Kind: modelcatalog.KindASR, Enabled: true,
		}, nil
	})
	handler := NewAudio(resolver, runner, client)
	body, contentType := multipartBodyAudio("public-asr", map[string][]byte{"file": {0x01, 0x02, 0x03}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), responseBody) {
		t.Fatalf("response = %d %s", response.Code, response.Body.Bytes())
	}
	if !lease.released {
		t.Fatal("successful attempt lease was not released")
	}
	got := <-captured
	if got.header.Get("Authorization") != "Bearer upstream-secret" {
		t.Fatalf("Authorization = %q", got.header.Get("Authorization"))
	}
	// Upstream body must carry the mapped model, not the public one.
	if !bytes.Contains(got.body, []byte("vendor/asr")) {
		t.Fatalf("mapped model missing: %s", got.body)
	}
	if bytes.Contains(got.body, []byte("public-asr")) {
		t.Fatalf("public model leaked upstream: %s", got.body)
	}
}

func TestAudioCleansReplayFileWhenMultipartCleanupFails(t *testing.T) {
	t.Setenv("GODEBUG", "multipartfiles=distinct")
	tempDir := t.TempDir()
	largePayload := bytes.Repeat([]byte{0x5a}, (1<<20)+1)
	body, contentType := multipartBodyAudioFiles("public-asr", [][]byte{largePayload, largePayload})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}
	files := request.MultipartForm.File["file"]
	if len(files) != 2 {
		t.Fatalf("file parts = %d, want 2", len(files))
	}
	second, err := files[1].Open()
	if err != nil {
		t.Fatalf("open second file: %v", err)
	}
	named, ok := second.(interface{ Name() string })
	if !ok {
		_ = second.Close()
		t.Fatal("large multipart file is not backed by a named file")
	}
	path := named.Name()
	if err := second.Close(); err != nil {
		t.Fatalf("close second file: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove second multipart file: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("replace second multipart file with directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("make replacement directory non-empty: %v", err)
	}

	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelKindMismatch
	})
	handler := NewAudio(resolver, nil, nil, tempDir)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("response status = %d, want 500", response.Code)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read configured temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("configured temp dir retains %d replay files", len(entries))
	}
}

func multipartBodyAudio(model string, parts map[string][]byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if model != "" {
		_ = writer.WriteField("model", model)
	}
	for name, data := range parts {
		if name == "file" {
			part, _ := writer.CreateFormFile("file", "audio.wav")
			_, _ = part.Write(data)
		} else {
			_ = writer.WriteField(name, string(data))
		}
	}
	_ = writer.Close()
	return &buf, writer.FormDataContentType()
}

func multipartBodyAudioFiles(model string, files [][]byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", model)
	for _, data := range files {
		part, _ := writer.CreateFormFile("file", "audio.wav")
		_, _ = part.Write(data)
	}
	_ = writer.Close()
	return &buf, writer.FormDataContentType()
}

func TestSpeechRequiresInput(t *testing.T) {
	handler := NewSpeech(nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"public-tts"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusBadRequest, "missing_required_parameter")
}

func TestSpeechMapsModelPrimesBodyAndFiltersContentType(t *testing.T) {
	tests := []struct {
		upstreamContentType string
		wantContentType     string
	}{
		{"audio/mpeg", "audio/mpeg"},
		{"audio/wav; rate=24000", "audio/wav"},
		{"text/plain", "application/octet-stream"},
		{"", "application/octet-stream"},
	}
	for _, test := range tests {
		t.Run(test.upstreamContentType, func(t *testing.T) {
			var capturedBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				capturedBody, _ = io.ReadAll(request.Body)
				if test.upstreamContentType != "" {
					writer.Header().Set("Content-Type", test.upstreamContentType)
				}
				_, _ = writer.Write([]byte{0x01, 0x02, 0x03})
			}))
			t.Cleanup(upstream.Close)

			descriptor := nvidia.DefaultDescriptor()
			descriptor.TTS.URL = upstream.URL + "/v1/audio/speech"
			client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			lease := &releaseTrackingLease{id: 12}
			runner := attemptRunnerFunc(func(ctx context.Context, _ int64, stream bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
				if !stream {
					t.Fatal("speech attempt was not marked streaming")
				}
				response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
				return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
			})
			resolver := modelResolverFunc(func(_ context.Context, publicID string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
				if publicID != "public-tts" || requirements.Kind != modelcatalog.KindTTS {
					t.Fatalf("resolve = %q, %#v", publicID, requirements)
				}
				return modelcatalog.Model{ID: 32, PublicID: publicID, UpstreamID: "vendor/tts", Kind: modelcatalog.KindTTS, Enabled: true}, nil
			})
			handler := NewSpeech(resolver, runner, client)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{
				"model":"public-tts","input":"sensitive input","voice":"alloy","response_format":"mp3"
			}`))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), []byte{0x01, 0x02, 0x03}) {
				t.Fatalf("response = %d %#v", response.Code, response.Body.Bytes())
			}
			if response.Header().Get("Content-Type") != test.wantContentType {
				t.Fatalf("content-type = %q", response.Header().Get("Content-Type"))
			}
			if got := response.Header().Get("X-Accel-Buffering"); got != "no" {
				t.Fatalf("X-Accel-Buffering = %q, want %q (audit B6: nginx must not buffer speech stream)", got, "no")
			}
			if !lease.released {
				t.Fatal("successful speech lease was not released")
			}
			if bytes.Contains(capturedBody, []byte("public-tts")) || !bytes.Contains(capturedBody, []byte("vendor/tts")) {
				t.Fatalf("mapped body = %s", capturedBody)
			}
		})
	}
}

func TestSpeechRejectsOversizedJSON(t *testing.T) {
	handler := NewSpeech(nil, nil, nil)
	body := strings.NewReader(`{"model":"public-tts","input":"` + strings.Repeat("x", 32<<20) + `"}`)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", body)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

// TestWriteChatErrorMapsBodyTooLarge verifies the replay-overflow sentinel from
// router.NewReplayableBody is surfaced to the client as 413 request_too_large
// rather than the generic 500 internal_error fallback.
func TestWriteChatErrorMapsBodyTooLarge(t *testing.T) {
	_, err := router.NewReplayableBody(make([]byte, (25<<20)+1), t.TempDir())
	if err == nil {
		t.Fatal("expected oversized replay body to be rejected")
	}
	response := httptest.NewRecorder()
	writeChatError(response, err)
	assertChatError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestSpeechDoesNotRetryAfterFirstAudioByte(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		writer.Header().Set("Content-Type", "audio/mpeg")
		_, _ = writer.Write([]byte{0x7f})
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.TTS.URL = upstream.URL + "/v1/audio/speech"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 13}
	var commit *router.CommitState
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		commit = &router.CommitState{}
		response, err := execute(ctx, lease.id, []byte("first-key"), commit)
		if err != nil {
			response, err = execute(ctx, lease.id+1, []byte("second-key"), &router.CommitState{})
		}
		return router.AttemptResult{Response: response, Lease: lease, Commit: commit, Attempts: upstreamCalls}, err
	})

	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 33, UpstreamID: "vendor/tts", Kind: modelcatalog.KindTTS, Enabled: true}, nil
	})
	handler := NewSpeech(resolver, runner, client)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"public-tts","input":"hello"}`))
	handler.ServeHTTP(response, request)

	if upstreamCalls != 1 {
		t.Fatalf("upstream attempts = %d, want 1", upstreamCalls)
	}
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), []byte{0x7f}) {
		t.Fatalf("response = %d %#v", response.Code, response.Body.Bytes())
	}
	if commit == nil || !commit.Committed() {
		t.Fatal("TTS first-byte output did not commit Attempt state")
	}
}
