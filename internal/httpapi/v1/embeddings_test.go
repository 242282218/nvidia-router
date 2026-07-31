package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/config"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
	"nvidia-router/internal/upstream/nvidia"
)

func TestEmbeddingsRejectsOversizedBody(t *testing.T) {
	handler := NewEmbeddings(nil, nil, nil)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/embeddings",
		strings.NewReader(strings.Repeat("x", config.JSONBodyLimit+1)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestEmbeddingsRejectsMissingInput(t *testing.T) {
	handler := NewEmbeddings(nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"public-embed"}`))
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusBadRequest, "missing_required_parameter")
}

func TestEmbeddingsRejectsNonEmbeddingModelKind(t *testing.T) {
	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelKindMismatch
	})
	handler := NewEmbeddings(resolver, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"public-embed","input":"hi"}`))
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusNotImplemented, "not_implemented")
}

func TestEmbeddingsMapsModelAndPreservesValidatedResponse(t *testing.T) {
	type capturedRequest struct {
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 1)
	responseBody := []byte(`{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured <- capturedRequest{header: request.Header.Clone(), body: body}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(responseBody)
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Embedding.URL = upstream.URL + "/v1/embeddings"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 9}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
		if publicID != "public-embed" || requirements.Kind != modelcatalog.KindEmbedding {
			t.Fatalf("resolve = %q, %#v", publicID, requirements)
		}
		return modelcatalog.Model{
			ID: 21, PublicID: publicID, UpstreamID: "vendor/embed", Kind: modelcatalog.KindEmbedding, Enabled: true,
		}, nil
	})
	handler := NewEmbeddings(resolver, runner, client)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"public-embed","input":"hi","encoding_format":"float"}`))
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !jsonEqual(response.Body.Bytes(), responseBody) {
		t.Fatalf("response = %d %s", response.Code, response.Body.Bytes())
	}
	if !lease.released {
		t.Fatal("successful attempt lease was not released")
	}
	got := <-captured
	if got.header.Get("Authorization") != "Bearer upstream-secret" {
		t.Fatalf("Authorization = %q", got.header.Get("Authorization"))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got.body, &fields); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if string(fields["model"]) != `"vendor/embed"` {
		t.Fatalf("model = %s", fields["model"])
	}
	if string(fields["encoding_format"]) != `"float"` {
		t.Fatalf("encoding_format = %s", fields["encoding_format"])
	}
}

func TestEmbeddingsExecutionSurfacesProtocolErrorForFailover(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"usage":{}}`)),
		}, nil
	})}
	client, err := nvidia.NewClient(httpClient, nvidia.DefaultDescriptor(), testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := NewEmbeddings(nil, nil, client).execute([]byte(`{"model":"m","input":"hi"}`))(
		context.Background(), 1, []byte("upstream-secret"), &router.CommitState{},
	)
		if response != nil {
			defer func() { _ = response.Body.Close() }()
		}
	if err == nil {
		t.Fatal("expected protocol error for malformed 2xx embeddings response")
	}
	if !errors.Is(err, nvidia.ErrProtocol) {
		t.Fatalf("error = %v, want protocol fault", err)
	}
}

func jsonEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	normalizedA, _ := json.Marshal(left)
	normalizedB, _ := json.Marshal(right)
	return string(normalizedA) == string(normalizedB)
}
