package v1

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	client, err := nvidia.NewClient(upstream.Client(), descriptor)
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
