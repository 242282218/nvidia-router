package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// OpenCodeFreeChat is the chat surface of the optional OpenCodeFree gateway. It
// has no NVIDIA key pool, so the router talks to it directly instead of through
// Attempt; the shared HTTP transport still bounds connect and response windows.
type OpenCodeFreeChat interface {
	Chat(context.Context, runtimeconfig.Snapshot, []byte, bool) (*http.Response, error)
}

type Chat struct {
	models       ModelResolver
	attempts     AttemptRunner
	client       provider.Provider
	openCodeFree OpenCodeFreeChat // optional; nil keeps NVIDIA-only routing
}

func NewChat(models ModelResolver, attempts AttemptRunner, client provider.Provider) *Chat {
	return &Chat{models: models, attempts: attempts, client: client}
}

// WithOpenCodeFree attaches the optional OpenCodeFree gateway client. Models
// whose provider is opencodefree are routed here instead of the NVIDIA key pool.
func (h *Chat) WithOpenCodeFree(client OpenCodeFreeChat) *Chat {
	h.openCodeFree = client
	return h
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
	// Parse already resolved the requested reasoning level (compat.ParseReasoning
	// ran on the request fields); re-parsing the payload for it would duplicate a
	// full-body unmarshal of every request, amplified by large image payloads.
	requestedReasoningLevel := parsed.RequestedReasoningLevel()
	observability.SetReasoningLevels(request.Context(), requestedReasoningLevel, "")
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
	// One pass over the upstream body extracts both the effective level and the
	// reasoning wire fields (previously two full-body unmarshals).
	effectiveReasoningLevel, reasoningRequested, wireFields := observability.ReasoningMetadataFromBody(upstreamBody)
	observability.SetReasoningLevels(request.Context(), requestedReasoningLevel, effectiveReasoningLevel)
	observability.SetReasoningRequest(request.Context(), reasoningRequested, wireFields)
	stream := parsed.Stream()
	// OpenCodeFree models route to the optional gateway directly: no NVIDIA key
	// pool, no failover, one upstream. Every other provider keeps the Attempt
	// path (key pool, retry budget, sticky sessions).
	if model.Provider == modelcatalog.ProviderOpenCodeFree {
		h.serveOpenCodeFree(writer, request, upstreamBody, stream)
		return
	}
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
				if errors.Is(err, sse.ErrNoSemanticData) {
					return response, fault.EmptyResponse(err)
				}
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
			if errors.Is(err, nvidia.ErrEmptyResponse) {
				return response, fault.EmptyResponse(err)
			}
			if errors.Is(err, nvidia.ErrProtocol) {
				return response, fault.Protocol(err)
			}
			return response, err
		}
		if present, chars := observability.ReasoningContentFromBody(validated.Body); present {
			observability.SetReasoningResponse(ctx, present, chars)
		}
		markResponseComplete(response)
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(validated.Body))
		response.ContentLength = int64(len(validated.Body))
		return response, nil
	}
}

// openCodeFreeRequestTimeout bounds a single OpenCodeFree gateway call. The
// gateway is a local/trusted endpoint with no key pool, so a hung upstream is
// bounded by an absolute window instead of the NVIDIA retry machinery.
const openCodeFreeRequestTimeout = 10 * time.Minute

// serveOpenCodeFree runs a chat request against the optional OpenCodeFree
// gateway. It mirrors the NVIDIA path's stream/nonstream handling but skips the
// key pool: the model's provider decides the route before this is called.
func (h *Chat) serveOpenCodeFree(writer http.ResponseWriter, request *http.Request, body []byte, stream bool) {
	if h.openCodeFree == nil {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusServiceUnavailable, Type: "server_error", Code: "provider_unconfigured",
			Message: "The OpenCodeFree gateway is not configured.",
		})
		return
	}
	// The gateway occasionally answers 502/503/504 on a transient upstream blip
	// (observed in round-2 stability runs). Non-stream requests retry once after
	// a short pause; streams never retry because headers may already be written.
	for attempt := 0; ; attempt++ {
		retryable := h.openCodeFreeOnce(writer, request, body, stream)
		if !retryable || attempt >= 1 || stream {
			return
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-request.Context().Done():
			return
		}
	}
}

func (h *Chat) openCodeFreeOnce(writer http.ResponseWriter, request *http.Request, body []byte, stream bool) (retryable bool) {
	ctx, cancel := context.WithTimeout(nvidia.WithForwardedHeaders(request.Context(), request.Header), openCodeFreeRequestTimeout)
	defer cancel()
	response, err := h.openCodeFree.Chat(ctx, runtimeconfig.Snapshot{}, body, stream)
	if err != nil {
		writeChatError(writer, fmt.Errorf("OpenCodeFree chat: %w", err))
		return false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		msg := strings.TrimSpace(string(bodyBytes))
		if msg == "" {
			msg = fmt.Sprintf("OpenCodeFree upstream returned HTTP %d", response.StatusCode)
		} else if len(msg) > 512 {
			msg = msg[:512]
		}
		if response.StatusCode == http.StatusNotFound {
			writeChatError(writer, &apierror.Error{
				Status: http.StatusBadGateway, Type: "server_error", Code: "upstream_model_not_found",
				Message: msg,
			})
			return false
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusGatewayTimeout {
			// 502/503/504 are transient; the non-stream caller retries once. 429 is
			// the gateway's own concurrency limit — retrying would only add load —
			// and a streamed response can never be retried, so both surface the
			// error immediately.
			retryable = !stream && response.StatusCode != http.StatusTooManyRequests
			if !retryable {
				writeChatError(writer, fault.New(response.StatusCode, fault.ScopeUpstreamGlobal, "server_error", "upstream_unavailable", msg, nil))
			}
			return retryable
		}
		writeChatError(writer, &apierror.Error{
			Status: http.StatusBadGateway, Type: "server_error", Code: "upstream_error",
			Message: msg,
		})
		return false
	}
	if stream {
		// No Attempt budget exists on this path, so primeSSE's idle wrapper would
		// get a zero window and tear the stream down immediately. Instead the
		// absolute context deadline above plus the transport timeouts bound it,
		// and streamResponse derives its write stall window from ctx.
		h.streamResponse(ctx, writer, response)
		return false
	}
	validated, err := nvidia.ValidateNonstreamChat(response)
	if err != nil {
		if errors.Is(err, nvidia.ErrEmptyResponse) || errors.Is(err, nvidia.ErrProtocol) {
			// A 200 response with an empty or malformed body can be a transient
			// gateway/upstream blip. Retry non-stream requests before surfacing it;
			// streams cannot be replayed after headers may have been sent.
			if !stream {
				return true
			}
		}
		if errors.Is(err, nvidia.ErrEmptyResponse) {
			writeChatError(writer, fault.EmptyResponse(err))
			return false
		}
		if errors.Is(err, nvidia.ErrProtocol) {
			writeChatError(writer, fault.Protocol(err))
			return false
		}
		writeChatError(writer, err)
		return false
	}
	markResponseComplete(response)
	_ = response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(validated.Body))
	response.ContentLength = int64(len(validated.Body))
	copyResponseHeaders(writer.Header(), response.Header)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
	return false
}

func (h *Chat) streamResponse(ctx context.Context, writer http.ResponseWriter, upstream *http.Response) {
	commit := &router.CommitState{}
	// Reasoning length sampling is gated on the request having asked for it;
	// non-reasoning streams never pay per-event JSON parsing. The gate is read
	// once so the snapshot mutex is not taken per SSE event.
	trackReasoning := observability.ReasoningRequested(ctx)
	var reasoningPresent bool
	var reasoningChars int64
	opts := sse.ProxyOptions{
		CommitState: commit,
		OnComplete: func() {
			observability.SetStreamDone(ctx, true)
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
	}
	if trackReasoning {
		// Accumulate reasoning_content character counts from the proxied deltas
		// without ever retaining their text.
		opts.OnData = func(data string) {
			if present, chars := observability.ReasoningDeltaChars([]byte(data)); present {
				reasoningPresent = true
				reasoningChars += chars
			}
		}
	}
	err := sse.Proxy(ctx, commit.Wrap(writer), upstream, opts)
	observability.SetReasoningResponse(ctx, reasoningPresent, reasoningChars)
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
	if err := sse.PrimeUntil(primeCtx, response, semanticChatEvent); err != nil {
		return err
	}
	// After the headers are committed a stalled upstream would pin the lease
	// forever; wrap the body so silence beyond the idle window returns
	// ErrStreamIdle instead of blocking the decode loop indefinitely.
	response.Body = sse.WithIdleTimeout(response.Body, idle)
	return nil
}

func semanticChatEvent(event sse.Event) (bool, error) {
	data := strings.TrimSpace(sse.JoinData(event.Data))
	if data == "" {
		return false, nil
	}
	if data == "[DONE]" {
		return false, sse.ErrNoSemanticData
	}
	
	// Fast path: check if the data contains any reasoning-related fields before
	// full JSON parsing. This avoids expensive unmarshaling for most chunks that
	// only contain content deltas.
	dataBytes := []byte(data)
	hasReasoningFields := bytes.Contains(dataBytes, []byte(`"reasoning_content"`)) ||
		bytes.Contains(dataBytes, []byte(`"reasoning"`)) ||
		bytes.Contains(dataBytes, []byte(`"thinking"`)) ||
		bytes.Contains(dataBytes, []byte(`"tool_calls"`))
	
	// If no reasoning fields are present, we only need to check for content
	if !hasReasoningFields {
		// Quick check for non-empty content field
		if bytes.Contains(dataBytes, []byte(`"content"`)) {
			return true, nil
		}
		return false, nil
	}
	
	// Slow path: full JSON parsing when reasoning fields are present
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          json.RawMessage   `json:"content"`
				ReasoningContent json.RawMessage   `json:"reasoning_content"`
				Reasoning        json.RawMessage   `json:"reasoning"`
				Thinking         json.RawMessage   `json:"thinking"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(dataBytes, &chunk); err != nil {
		return false, fmt.Errorf("decode upstream chat event: %w", err)
	}
	for _, choice := range chunk.Choices {
		if hasSSETextValue(choice.Delta.Content) ||
			hasSSETextValue(choice.Delta.ReasoningContent) ||
			hasSSETextValue(choice.Delta.Reasoning) ||
			hasSSETextValue(choice.Delta.Thinking) ||
			len(choice.Delta.ToolCalls) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func hasSSETextValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return text != ""
	}
	var nested struct {
		Thought string `json:"thought"`
		Text    string `json:"text"`
	}
	if json.Unmarshal(trimmed, &nested) == nil {
		if nested.Thought != "" || nested.Text != "" {
			return true
		}
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &parts) != nil {
		return false
	}
	for _, part := range parts {
		if part.Text != "" {
			return true
		}
	}
	return false
}

func markResponseComplete(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	if marker, ok := response.Body.(interface{ MarkComplete() }); ok {
		marker.MarkComplete()
	}
}
