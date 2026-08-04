package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/observability"
	responsesprotocol "nvidia-router/internal/protocol/responses"
	"nvidia-router/internal/router"
	"nvidia-router/internal/sse"
	"nvidia-router/internal/upstream/nvidia"
)

// Responses proxies OpenAI Responses API requests by translating them to
// Chat Completions upstream and back. It shares the same ModelCatalog, Pool,
// Attempt orchestrator and NVIDIA Chat client as the Chat handler, so no
// parallel key scheduling path exists.
type Responses struct {
	models   ModelResolver
	attempts AttemptRunner
	client   *nvidia.Client
}

func NewResponses(models ModelResolver, attempts AttemptRunner, client *nvidia.Client) *Responses {
	return &Responses{models: models, attempts: attempts, client: client}
}

func (h *Responses) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
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
	modelID, stream, err := parseResponsesHeader(payload)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	observability.SetModel(request.Context(), modelID, stream)
	model, err := h.models.Resolve(request.Context(), modelID, chatModelRequirements())
	if err != nil {
		writeChatError(writer, modelError(err))
		return
	}
	upstreamBody, err := responsesprotocol.ToChat(payload, model)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	id, err := responsesprotocol.NewResponseID()
	if err != nil {
		writeChatError(writer, err)
		return
	}
	result, err := h.attempts.Run(request.Context(), model.ID, stream, h.execute(upstreamBody, id, model, stream))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer func() { _ = result.Response.Body.Close() }()

	if stream {
		h.streamResponse(request.Context(), writer, result.Response, id, model.PublicID)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(writer, result.Response.Body)
}

func (h *Responses) execute(body []byte, responsesID string, model modelcatalog.Model, stream bool) router.ExecuteFunc {
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
		converted, err := responsesprotocol.FromChat(validated.Body, responsesID, model)
		if err != nil {
			return response, fault.Protocol(fmt.Errorf("convert NVIDIA chat response: %w", err))
		}
		response.Body = io.NopCloser(bytes.NewReader(converted))
		response.ContentLength = int64(len(converted))
		return response, nil
	}
}

// streamResponse drives the Responses state machine over the upstream Chat SSE
// and writes the translated Responses event sequence to the client. The first
// event commits the response so later upstream failures cannot trigger a key
// switch; an interruption after commit emits a stable response.failed terminal.
func (h *Responses) streamResponse(ctx context.Context, writer http.ResponseWriter, upstream *http.Response, responseID, model string) {
	commit := &router.CommitState{}
	emitter := &responsesSSEEmitter{
		encoder: sse.NewEncoder(commit.Wrap(writer)),
		commit:  commit,
		flusher: writer,
		header:  false,
	}
	source := &chatDeltaSource{decoder: sse.NewDecoder(upstream.Body)}
	cancelDone := make(chan struct{})
	defer close(cancelDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = upstream.Body.Close()
		case <-cancelDone:
		}
	}()
	interrupted, err := responsesprotocol.Stream(source, emitter, responseID, model)
	if err != nil {
		// After commit the terminal is already written; before commit surface a
		// public error since nothing has reached the client.
		if !commit.Committed() {
			writeChatError(writer, &apierror.Error{
				Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
				Message: "The server could not complete the stream.",
			})
		}
		return
	}
	// ErrStreamInterrupted was already mapped by the state machine to a stable
	// response.failed terminal plus a single [DONE], so nothing more to do.
	_ = interrupted
}

// chatDeltaSource adapts the upstream SSE decoder to the state machine's
// ChatDeltaSource by decoding each event's data payload with ParseChatDelta.
// The terminal [DONE] marker is reported as end-of-stream so the state machine
// finalises.
type chatDeltaSource struct {
	decoder *sse.Decoder
	done    bool
}

func (c *chatDeltaSource) Next() (responsesprotocol.ChatDelta, error) {
	if c.done {
		return responsesprotocol.ChatDelta{}, responsesprotocol.ErrStreamCompleted
	}
	for {
		event, err := c.decoder.Decode()
		if err == io.EOF {
			return responsesprotocol.ChatDelta{}, responsesprotocol.ErrStreamInterrupted
		}
		if err != nil {
			return responsesprotocol.ChatDelta{}, fmt.Errorf("decode upstream SSE event: %w", err)
		}
		if len(event.Data) > 0 {
			delta, done, perr := responsesprotocol.ParseChatDelta([]byte(sse.JoinData(event.Data)))
			if perr != nil {
				return responsesprotocol.ChatDelta{}, fmt.Errorf("parse chat delta: %w", perr)
			}
			if done {
				// [DONE] is the authoritative normal completion marker. Keep it
				// distinct from EOF/interruption, and ignore duplicate markers.
				c.done = true
				return responsesprotocol.ChatDelta{}, responsesprotocol.ErrStreamCompleted
			}
			return delta, nil
		}
		// Comments or events with no data frames: skip and read the next.
	}
}

// responsesSSEEmitter writes Responses events to the HTTP response and commits
// on the first event. The done event is rendered as the final [DONE] marker.
type responsesSSEEmitter struct {
	encoder *sse.Encoder
	commit  *router.CommitState
	flusher http.ResponseWriter
	header  bool
}

func (e *responsesSSEEmitter) Emit(event responsesprotocol.EmittedEvent) error {
	if !e.header {
		writeSSEHeaders(e.flusher)
		e.commit.Wrap(e.flusher).WriteHeader(http.StatusOK)
		e.header = true
	}
	if event.Event == "done" {
		if err := e.encoder.Encode(sse.Event{Data: []string{"[DONE]"}}); err != nil {
			return fmt.Errorf("encode done: %w", err)
		}
	} else {
		payload, err := json.Marshal(event.Data)
		if err != nil {
			return fmt.Errorf("marshal responses event: %w", err)
		}
		if err := e.encoder.Encode(sse.Event{Event: event.Event, Data: []string{string(payload)}}); err != nil {
			return fmt.Errorf("encode responses event: %w", err)
		}
	}
	if flusher, ok := e.flusher.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (e *responsesSSEEmitter) Commit() error {
	if !e.header {
		writeSSEHeaders(e.flusher)
		e.header = true
		e.commit.Wrap(e.flusher).WriteHeader(http.StatusOK)
	}
	return nil
}

func writeSSEHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	// Disable nginx-style reverse proxy buffering for SSE so chunks reach the
	// client live (audit B6). Non-nginx proxies ignore the header.
	writer.Header().Set("X-Accel-Buffering", "no")
}

// parseResponsesHeader extracts the model and stream fields in a single pass
// over the request body so Resolve and the attempt runner know how to route
// before ToChat re-validates the bound model. The previous split ran two
// full-body json.Unmarshal calls ahead of ToChat's own parse, scanning 32 MiB
// bodies three times.
func parseResponsesHeader(body []byte) (modelID string, stream bool, err error) {
	var header struct {
		Model  string          `json:"model"`
		Stream json.RawMessage `json:"stream"`
	}
	// A malformed body surfaces as the model error exactly as it did when
	// extractResponsesModel ran first, so clients see one stable failure.
	if unmarshalErr := json.Unmarshal(body, &header); unmarshalErr != nil || header.Model == "" {
		return "", false, &apierror.Error{
			Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_parameter",
			Message: "The model parameter must be a non-empty string.",
		}
	}
	if len(header.Stream) == 0 {
		return header.Model, false, nil
	}
	var value bool
	if err := json.Unmarshal(header.Stream, &value); err != nil {
		return "", false, &apierror.Error{
			Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_parameter",
			Message: "The stream parameter must be a boolean.",
		}
	}
	return header.Model, value, nil
}

func chatModelRequirements() modelcatalog.Requirements {
	return modelcatalog.Requirements{Kind: modelcatalog.KindChat}
}
