package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/modelcatalog"
	responsesprotocol "nvidia-router/internal/protocol/responses"
	"nvidia-router/internal/router"
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
	payload, err := readChatBody(writer, request)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	modelID, err := extractResponsesModel(payload)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	stream, err := detectResponsesStream(payload)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	if stream {
		// Streaming translation is wired in the Responses SSE state machine task.
		writeChatError(writer, &apierror.Error{
			Status: http.StatusNotImplemented, Type: "invalid_request_error", Code: "not_implemented",
			Message: "Streaming Responses are not implemented.",
		})
		return
	}
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
	result, err := h.attempts.Run(request.Context(), model.ID, false, h.execute(upstreamBody, id, model))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer result.Response.Body.Close()

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(writer, result.Response.Body)
}

func (h *Responses) execute(body []byte, responsesID string, model modelcatalog.Model) router.ExecuteFunc {
	return func(ctx context.Context, _ int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		response, err := h.client.Chat(ctx, snapshotFromBudget(ctx), string(secret), body, false)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
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
			return response, err
		}
		response.Body = io.NopCloser(bytes.NewReader(converted))
		response.ContentLength = int64(len(converted))
		return response, nil
	}
}

// extractResponsesModel pulls the model field so Resolve can locate the
// whitelist entry before ToChat re-validates the bound model identity.
func extractResponsesModel(body []byte) (string, error) {
	var fields struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &fields); err != nil || fields.Model == "" {
		return "", &apierror.Error{
			Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_parameter",
			Message: "The model parameter must be a non-empty string.",
		}
	}
	return fields.Model, nil
}

func detectResponsesStream(body []byte) (bool, error) {
	var fields struct {
		Stream json.RawMessage `json:"stream,omitempty"`
	}
	_ = json.Unmarshal(body, &fields)
	if len(fields.Stream) == 0 {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(fields.Stream, &value); err != nil {
		return false, &apierror.Error{
			Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_parameter",
			Message: "The stream parameter must be a boolean.",
		}
	}
	return value, nil
}

func chatModelRequirements() modelcatalog.Requirements {
	return modelcatalog.Requirements{Kind: modelcatalog.KindChat}
}
