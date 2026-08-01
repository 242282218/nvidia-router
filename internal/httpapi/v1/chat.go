package v1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/config"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/observability"
	chatprotocol "nvidia-router/internal/protocol/chat"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/sse"
	"nvidia-router/internal/upstream/nvidia"
	"nvidia-router/internal/xkproxy"
)

// maxConcurrentBodyReads bounds how many requests may be reading their body at
// once. Each body can be up to 32 MiB, so without a cap a burst of concurrent
// /v1 requests would buffer N x 32 MiB before pool admission ever applies.
const maxConcurrentBodyReads = 16

// bodyReadSemaphore gates body reads ahead of pool admission so request
// concurrency cannot bypass the pool's resource limits.
var bodyReadSemaphore = make(chan struct{}, maxConcurrentBodyReads)

type ModelResolver interface {
	Resolve(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error)
}

type AttemptRunner interface {
	Run(context.Context, int64, bool, router.ExecuteFunc) (router.AttemptResult, error)
}

type Chat struct {
	models   ModelResolver
	attempts AttemptRunner
	client   *nvidia.Client
}

func NewChat(models ModelResolver, attempts AttemptRunner, client *nvidia.Client) *Chat {
	return &Chat{models: models, attempts: attempts, client: client}
}

func (h *Chat) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusMethodNotAllowed, Type: "invalid_request_error", Code: "method_not_allowed",
			Message: "Only POST is supported for this endpoint.",
		})
		return
	}
	payload, err := readChatBody(writer, request)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	parsed, err := chatprotocol.Parse(payload)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	observability.SetModel(request.Context(), parsed.PublicModelID(), parsed.Stream())
	model, err := h.models.Resolve(request.Context(), parsed.PublicModelID(), parsed.Requirements())
	if err != nil {
		writeChatError(writer, modelError(err))
		return
	}
	upstreamBody, err := parsed.MarshalFor(model)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	stream := parsed.Stream()
	result, err := h.attempts.Run(request.Context(), model.ID, stream, h.execute(upstreamBody, stream))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer func() { _ = result.Response.Body.Close() }()

	if stream {
		h.streamResponse(request.Context(), writer, result.Response)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(writer, result.Response.Body)
}

func (h *Chat) execute(body []byte, stream bool) router.ExecuteFunc {
	return func(ctx context.Context, _ int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		response, err := h.client.Chat(ctx, snapshotFromBudget(ctx), string(secret), body, stream)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return response, nil
		}
		if stream {
			if err := primeSSE(ctx, response); err != nil {
				if errors.Is(err, sse.ErrEventTooLarge) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					// A 200 with an empty/non-SSE body is an upstream protocol
					// defect, not a connection blip; classify it as Protocol so
					// the attempt loop does not cool down every healthy key.
					return response, fault.Protocol(err)
				}
				return response, err
			}
			return response, nil
		}
		validated, err := nvidia.ValidateNonstreamChat(response)
		if err != nil {
			if errors.Is(err, nvidia.ErrProtocol) {
				return response, fault.Protocol(err)
			}
			return response, err
		}
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(validated.Body))
		response.ContentLength = int64(len(validated.Body))
		return response, nil
	}
}

func (h *Chat) streamResponse(ctx context.Context, writer http.ResponseWriter, upstream *http.Response) {
	commit := &router.CommitState{}
	err := sse.Proxy(ctx, commit.Wrap(writer), upstream, sse.ProxyOptions{CommitState: commit})
	if err == nil || err == sse.ErrStreamInterrupted {
		// Both clean completion and interrupted stream: connection already committed,
		// client observes truncation. Nothing more to write.
		return
	}
	// Context cancelled or other error after commit - nothing we can do.
	// Before commit, writeChatError handles it.
	if !commit.Committed() {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
			Message: "The server could not complete the stream.",
		})
	}
}

func readChatBody(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	// Acquire the read slot before touching the body; a burst of requests must
	// not buffer N x 32 MiB while waiting for pool admission. Refuse with 429
	// when saturated instead of queueing goroutines behind slow uploads.
	select {
	case bodyReadSemaphore <- struct{}{}:
		defer func() { <-bodyReadSemaphore }()
	default:
		return nil, &apierror.Error{
			Status: http.StatusTooManyRequests, Type: "server_error", Code: "server_busy",
			Message: "The server is busy reading request bodies, try again later.",
		}
	}
	// Pass a nil writer to MaxBytesReader so the handler alone writes the
	// JSON error body; a non-nil writer would emit a plain-text 413 first and
	// corrupt the JSON response contract.
	body, err := io.ReadAll(http.MaxBytesReader(nil, request.Body, config.JSONBodyLimit))
	if err == nil {
		return body, nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		param := "body"
		return nil, &apierror.Error{
			Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
			Message: "The request body exceeds the 32 MiB limit.", Param: &param,
		}
	}
	return nil, &apierror.Error{
		Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_request",
		Message: "The request body could not be read.",
	}
}

func snapshotFromBudget(ctx context.Context) runtimeconfig.Snapshot {
	budget, ok := router.BudgetFromContext(ctx)
	if !ok {
		return runtimeconfig.Snapshot{}
	}
	return runtimeconfig.Snapshot{
		ConnectTimeoutMS:   int(budget.ConnectTimeout() / time.Millisecond),
		FirstByteTimeoutMS: int(budget.FirstByteTimeout() / time.Millisecond),
		FirstByteDeadline:  budget.FirstByteDeadline(),
	}
}

func modelError(err error) error {
	if errors.Is(err, modelcatalog.ErrModelNotFound) {
		return &apierror.Error{
			Status: http.StatusNotFound, Type: "invalid_request_error", Code: "model_not_found",
			Message: "The requested model is not available.",
		}
	}
	if errors.Is(err, modelcatalog.ErrModelKindMismatch) ||
		errors.Is(err, modelcatalog.ErrCapabilityUnsupported) ||
		errors.Is(err, modelcatalog.ErrCapabilityUnverified) {
		return &apierror.Error{
			Status: http.StatusNotImplemented, Type: "invalid_request_error", Code: "not_implemented",
			Message: "The requested model capability is not implemented.",
		}
	}
	return err
}

func writeChatError(writer http.ResponseWriter, err error) {
	var proxyErr *xkproxy.Error
	if errors.As(err, &proxyErr) {
		apierror.Error{
			Status: http.StatusBadGateway, Type: "server_error", Code: "upstream_proxy_unavailable",
			Message: "The upstream proxy is temporarily unavailable.",
		}.Write(writer)
		return
	}
	var publicError *apierror.Error
	if errors.As(err, &publicError) {
		publicError.Write(writer)
		return
	}
	var upstreamFault fault.Fault
	if errors.As(err, &upstreamFault) {
		apierror.Error{
			Status: upstreamFault.HTTPStatus, Type: upstreamFault.PublicType, Code: upstreamFault.PublicCode,
			Message: upstreamFault.PublicMessage, RetryAfter: upstreamFault.RetryAfter,
		}.Write(writer)
		return
	}
	apierror.Error{
		Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
		Message: "The server could not complete the request.",
	}.Write(writer)
}

func primeSSE(ctx context.Context, response *http.Response) error {
	primeCtx := ctx
	cancel := func() {}
	var idle time.Duration
	if budget, ok := router.BudgetFromContext(ctx); ok {
		primeCtx, cancel = context.WithDeadline(ctx, budget.FirstByteDeadline())
		idle = budget.FirstByteTimeout()
	}
	defer cancel()
	if err := sse.Prime(primeCtx, response); err != nil {
		return err
	}
	// After the headers are committed a stalled upstream would pin the lease
	// forever; wrap the body so silence beyond the first-byte window returns
	// ErrStreamIdle instead of blocking the decode loop indefinitely.
	response.Body = sse.WithIdleTimeout(response.Body, idle)
	return nil
}
