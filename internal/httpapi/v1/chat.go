package v1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/observability"
	chatprotocol "nvidia-router/internal/protocol/chat"
	"nvidia-router/internal/provider"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/sse"
	"nvidia-router/internal/upstream/nvidia"
	"nvidia-router/internal/xkproxy"
)

type ModelResolver interface {
	Resolve(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error)
}

type AttemptRunner interface {
	Run(context.Context, int64, bool, router.ExecuteFunc) (router.AttemptResult, error)
}

type Chat struct {
	models   ModelResolver
	attempts AttemptRunner
	client   provider.Provider
}

func NewChat(models ModelResolver, attempts AttemptRunner, client provider.Provider) *Chat {
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
	payload, bodyLease, err := readBodyWithLease(request, bodyReadLimitForJSON(), jsonBodyReadTimeout)
	if bodyLease != nil {
		defer bodyLease.Release()
	}
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
	model, err := h.models.Resolve(request.Context(), parsed.PublicModelID(), modelcatalog.Requirements{Kind: modelcatalog.KindChat})
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
	// Propagate per-model streaming timeout overrides so the budget builder
	// inside Attempt.Run uses the model's configured windows instead of the
	// global defaults. Matters most for deepseek-v4-flash, whose TTFT on
	// NVIDIA infrastructure routinely exceeds the fleet-wide default.
	runCtx := applyModelTimeouts(nvidia.WithForwardedHeaders(request.Context(), request.Header), model)
	result, err := h.attempts.Run(runCtx, model.ID, stream, h.execute(upstreamBody, stream))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer func() { _ = result.Response.Body.Close() }()

	if stream {
		ctx := result.Context
		if ctx == nil {
			ctx = request.Context()
		}
		h.streamResponse(ctx, writer, result.Response)
		return
	}

	copyResponseHeaders(writer.Header(), result.Response.Header)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(writer, result.Response.Body)
}

func (h *Chat) execute(body []byte, stream bool) router.ExecuteFunc {
	return func(ctx context.Context, keyID int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		ctx = nvidia.WithStickySession(ctx, keyID)
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
	err := sse.Proxy(ctx, commit.Wrap(writer), upstream, sse.ProxyOptions{
		CommitState: commit,
		OnComplete: func() {
			if marker, ok := upstream.Body.(interface{ MarkComplete() }); ok {
				marker.MarkComplete()
			}
		},
		// TTFT is the first SSE data event reaching the client, distinct from
		// the first-byte metric which also fires for error bodies before commit.
		OnFirstData: func() { observability.SetFirstTokenAt(ctx, time.Now()) },
		// Bound how long a flush may block on a client that stopped reading; the
		// request context only fires on disconnect, not on a connected-but-stalled
		// consumer, so without this a stuck client pins the credential slot until
		// the upstream closes (audit H6).
		WriteIdleTimeout: streamWriteDeadline(ctx),
	})
	if err == nil || (err == sse.ErrStreamInterrupted && commit.Committed()) || (err == sse.ErrStreamWriteStalled && commit.Committed()) {
		// Clean completion, an interrupted stream whose first byte already reached
		// the client, or a write stall after commit (the client stopped reading and
		// the stream was torn down to release the lease): the client observes
		// truncation and nothing more can be written.
		return
	}
	// Context cancelled or other error after commit - nothing we can do.
	// Before commit, writeChatError handles it. An interrupted stream that
	// never committed is an upstream protocol defect (200 with no data events):
	// surface an error rather than the empty 200 the client would take as a
	// successful completion.
	if !commit.Committed() {
		if errors.Is(err, sse.ErrStreamInterrupted) {
			writeChatError(writer, &apierror.Error{
				Status: http.StatusBadGateway, Type: "server_error", Code: "upstream_protocol_error",
				Message: "The upstream service ended the stream before sending any data.",
			})
			return
		}
		writeChatError(writer, &apierror.Error{
			Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
			Message: "The server could not complete the stream.",
		})
		return
	}
	// The stream committed but ended with an error outside the benign set above.
	// The client already has headers, so nothing more can be written; log so a
	// truncated generation is traceable instead of silently disappearing. A
	// client disconnect (context cancellation) is expected churn and stays at
	// Debug; upstream stalls and decode errors after commit are genuine faults.
	if ctx.Err() != nil {
		observability.RequestLogger(ctx).Debug("stream_context_cancelled_after_commit", "error", err)
		return
	}
	observability.RequestLogger(ctx).Warn("stream_truncated_after_commit", "error", err)
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

// streamWriteIdleTimeout returns the write-side stall window from the request
// budget. It mirrors the read-side idle timeout so a client that stops reading
// is torn down on the same cadence as an upstream that stops sending. The read
// and write windows are deliberately coupled: a long-running generation needs
// both sides to tolerate the same inter-token gaps.
func streamWriteIdleTimeout(ctx context.Context) time.Duration {
	budget, ok := router.BudgetFromContext(ctx)
	if !ok {
		return 0
	}
	return budget.StreamIdleTimeout()
}

func streamWriteDeadline(ctx context.Context) time.Duration {
	deadlineRemaining := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			deadlineRemaining = remaining
		} else {
			deadlineRemaining = time.Nanosecond
		}
	}
	if idle := streamWriteIdleTimeout(ctx); idle > 0 {
		if deadlineRemaining > 0 && deadlineRemaining < idle {
			return deadlineRemaining
		}
		return idle
	}
	return deadlineRemaining
}

// applyModelTimeouts returns a context with per-model streaming timeout hints
// when the model has non-nil override columns. The router's Attempt.Run merges
// these into the budget so the model's configured windows take precedence over
// the global runtime_settings defaults.
func applyModelTimeouts(ctx context.Context, model modelcatalog.Model) context.Context {
	if model.StreamFirstTokenTimeoutMS == nil && model.StreamIdleTimeoutMS == nil {
		return ctx
	}
	hints := runtimeconfig.ModelTimeouts{}
	if model.StreamFirstTokenTimeoutMS != nil {
		hints.StreamFirstTokenTimeoutMS = *model.StreamFirstTokenTimeoutMS
	}
	if model.StreamIdleTimeoutMS != nil {
		hints.StreamIdleTimeoutMS = *model.StreamIdleTimeoutMS
	}
	return runtimeconfig.WithModelTimeouts(ctx, hints)
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
	if router.BodyTooLarge(err) {
		apierror.Error{
			Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
			Message: "The request body exceeds the 25 MiB replay limit.",
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
		// The prime phase waits for the first SSE data event, so it is bounded by
		// the first-token window, not the transport-level first-byte window. The
		// idle guard that wraps the body below is the separate in-stream window.
		primeCtx, cancel = context.WithDeadline(ctx, budget.FirstTokenDeadline())
		idle = budget.StreamIdleTimeout()
	}
	defer cancel()
	if err := sse.Prime(primeCtx, response); err != nil {
		return err
	}
	// After the headers are committed a stalled upstream would pin the lease
	// forever; wrap the body so silence beyond the idle window returns
	// ErrStreamIdle instead of blocking the decode loop indefinitely.
	response.Body = sse.WithIdleTimeout(response.Body, idle)
	return nil
}
