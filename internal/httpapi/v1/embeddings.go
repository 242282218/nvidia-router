package v1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/embedcache"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/observability"
	embeddingsprotocol "nvidia-router/internal/protocol/embeddings"
	"nvidia-router/internal/provider"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
)

// Embeddings proxies OpenAI Embeddings requests through the same ModelCatalog,
// Pool, Attempt orchestrator and NVIDIA provider as the Chat and Responses
// handlers, so no parallel key scheduling path exists. It is non-streaming.
type Embeddings struct {
	models   ModelResolver
	attempts AttemptRunner
	client   provider.Provider
	// cache is an optional exact-match embedding cache. When non-nil and the
	// runtime setting is enabled, identical normalized upstream requests bypass
	// the upstream entirely.
	cache    *embedcache.Cache
	settings runtimeconfig.Provider
}

func NewEmbeddings(models ModelResolver, attempts AttemptRunner, client provider.Provider, settings runtimeconfig.Provider, cache *embedcache.Cache) *Embeddings {
	if settings == nil {
		settings = &noSettings{}
	}
	return &Embeddings{models: models, attempts: attempts, client: client, settings: settings, cache: cache}
}

// noSettings is a zero-value settings provider so tests that construct an
// Embeddings without one keep the cache feature off (safe default).
type noSettings struct{}

func (*noSettings) Snapshot() runtimeconfig.Snapshot { return runtimeconfig.Snapshot{} }

func (h *Embeddings) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusMethodNotAllowed, Type: "invalid_request_error", Code: "method_not_allowed",
			Message: "Only POST is supported for this endpoint.",
		})
		return
	}
	payload, bodyLease, err := readBodyWithLease(request, bodyReadLimitForJSON(), jsonBodyReadTimeout)
	if bodyLease != nil {
		defer bodyLease.Release()
	}
	if err != nil {
		writeChatError(writer, err)
		return
	}
	parsed, err := embeddingsprotocol.Parse(payload)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	observability.SetModel(request.Context(), parsed.PublicModelID(), false)
	model, err := h.models.Resolve(request.Context(), parsed.PublicModelID(), parsed.Requirements())
	if err != nil {
		writeChatError(writer, modelError(err))
		return
	}
	if !requireNVIDIAProvider(writer, model) {
		return
	}
	upstreamBody, err := parsed.MarshalFor(model)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	// Exact-match cache: identical normalized upstream requests short-circuit
	// the upstream. The cache is off by default and bounded; the mapped model
	// remains part of the hashed request body.
	cacheSettings := h.settings.Snapshot()
	cacheEnabled := h.cache != nil && cacheSettings.EmbeddingCacheEnabled
	if h.cache != nil {
		h.cache.Resize(cacheSettings.EmbeddingCacheMaxEntries)
	}
	var fingerprint string
	if cacheEnabled {
		fingerprint = embedcache.Fingerprint(upstreamBody)
		if cached, ok := h.cache.Get(fingerprint); ok {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("X-Embedding-Cache", "HIT")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(cached)
			return
		}
	}
	result, err := h.attempts.Run(request.Context(), model.ID, false, h.execute(upstreamBody))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer func() { _ = result.Response.Body.Close() }()

	if result.Response.StatusCode < http.StatusOK || result.Response.StatusCode >= http.StatusMultipleChoices {
		writeChatError(writer, &apierror.Error{
			Status: result.Response.StatusCode, Type: "upstream_error", Code: "upstream_error",
			Message: "The upstream service returned an error.",
		})
		return
	}
	body, err := io.ReadAll(result.Response.Body)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	// Only cache 2xx responses: a cached error would wrongfully hide an upstream
	// outage. The validated body (already parsed by execute) is safe to cache.
	if cacheEnabled {
		h.cache.Put(fingerprint, body)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = writer.Write(body)
}

func (h *Embeddings) execute(body []byte) router.ExecuteFunc {
	return func(ctx context.Context, keyID int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		ctx = nvidia.WithStickySession(ctx, keyID)
		response, err := h.client.Embeddings(ctx, snapshotFromBudget(ctx), string(secret), body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return response, nil
		}
		validated, err := nvidia.ValidateNonstreamEmbeddings(response)
		if err != nil {
			if errors.Is(err, nvidia.ErrProtocol) {
				return response, fault.Protocol(err)
			}
			return response, err
		}
		markResponseComplete(response)
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(validated.Body))
		response.ContentLength = int64(len(validated.Body))
		return response, nil
	}
}
