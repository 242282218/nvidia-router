package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"nvidia-router/internal/config"
	"nvidia-router/internal/embedcache"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
)

// settableSettings lets tests flip the runtime embedding-cache toggle.
type settableSettings struct {
	enabled bool
	size    int
}

func (s *settableSettings) Snapshot() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{EmbeddingCacheEnabled: s.enabled, EmbeddingCacheMaxEntries: s.size}
}

func TestEmbeddingsRejectsOversizedBody(t *testing.T) {
	handler := NewEmbeddings(nil, nil, nil, nil, nil)
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
	handler := NewEmbeddings(nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"public-embed"}`))
	handler.ServeHTTP(response, request)
	assertChatError(t, response, http.StatusBadRequest, "missing_required_parameter")
}

func TestEmbeddingsRejectsNonEmbeddingModelKind(t *testing.T) {
	resolver := modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelKindMismatch
	})
	handler := NewEmbeddings(resolver, nil, nil, nil, nil)
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
	handler := NewEmbeddings(resolver, runner, client, nil, nil)
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
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"usage":{}}`)
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Embedding.URL = upstream.URL + "/v1/embeddings"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := NewEmbeddings(nil, nil, client, nil, nil).execute([]byte(`{"model":"m","input":"hi"}`))(
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

// TestEmbeddingsCacheServersWithCacheOn returns the full response from cache
// after the first upstream call, and every repeat bypasses the upstream.
func TestEmbeddingsCacheHitsSecondRequest(t *testing.T) {
	var upstreamCalls atomic.Int64
	responseBody := []byte(`{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
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
	cache := embedcache.New(10)
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 21, PublicID: publicID, UpstreamID: "vendor/embed", Kind: modelcatalog.KindEmbedding, Enabled: true}, nil
	})
	settings := &settableSettings{enabled: true, size: 10}
	handler := NewEmbeddings(resolver, attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, 1, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Attempts: 1}, err
	}), client, settings, cache)

	body := `{"model":"public-embed","input":"same text"}`
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("cached request status = %d", response.Code)
		}
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (second request served from cache)", upstreamCalls.Load())
	}
}

func TestEmbeddingsCacheSeparatesResponseAffectingFields(t *testing.T) {
	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"embedding":[0.1]}]}`))
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Embedding.URL = upstream.URL + "/v1/embeddings"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 21, PublicID: publicID, UpstreamID: "vendor/embed", Kind: modelcatalog.KindEmbedding, Enabled: true}, nil
	})
	settings := &settableSettings{enabled: true, size: 10}
	handler := NewEmbeddings(resolver, attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, 1, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Attempts: 1}, err
	}), client, settings, embedcache.New(10))

	for _, body := range []string{
		`{"model":"public-embed","input":"same text","dimensions":256}`,
		`{"model":"public-embed","input":"same text","dimensions":512}`,
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("request status = %d", response.Code)
		}
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 for different dimensions", upstreamCalls.Load())
	}

	// The admin setting is runtime-configurable; reducing the entry cap must
	// evict the old LRU entry before the next cache lookup.
	settings.size = 1
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(
		`{"model":"public-embed","input":"same text","dimensions":256}`,
	))
	handler.ServeHTTP(response, request)
	if upstreamCalls.Load() != 3 {
		t.Fatalf("upstream calls after shrinking cache = %d, want 3", upstreamCalls.Load())
	}
}

// TestEmbeddingsCacheDisabledStillCallsUpstream confirms the runtime toggle
// keeps the cache off even when one is constructed.
func TestEmbeddingsCacheDisabledSkipsCache(t *testing.T) {
	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"embedding":[0.1]}]}`))
	}))
	t.Cleanup(upstream.Close)
	descriptor := nvidia.DefaultDescriptor()
	descriptor.Embedding.URL = upstream.URL + "/v1/embeddings"
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resolver := modelResolverFunc(func(_ context.Context, publicID string, _ modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{ID: 21, PublicID: publicID, UpstreamID: "vendor/embed", Kind: modelcatalog.KindEmbedding, Enabled: true}, nil
	})
	settings := &settableSettings{enabled: false, size: 10}
	handler := NewEmbeddings(resolver, attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, 1, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Attempts: 1}, err
	}), client, settings, embedcache.New(10))

	body := `{"model":"public-embed","input":"same text"}`
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("request status = %d", response.Code)
		}
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (cache disabled)", upstreamCalls.Load())
	}
}
