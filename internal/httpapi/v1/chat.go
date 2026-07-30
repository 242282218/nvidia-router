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
	chatprotocol "nvidia-router/internal/protocol/chat"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
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
	if parsed.Stream() {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusNotImplemented, Type: "invalid_request_error", Code: "not_implemented",
			Message: "Streaming Chat Completions are not implemented yet.",
		})
		return
	}
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
	result, err := h.attempts.Run(request.Context(), model.ID, false, h.execute(upstreamBody))
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

func (h *Chat) execute(body []byte) router.ExecuteFunc {
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
		response.Body = io.NopCloser(bytes.NewReader(validated.Body))
		response.ContentLength = int64(len(validated.Body))
		return response, nil
	}
}

func readChatBody(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, config.JSONBodyLimit))
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
	return runtimeconfig.Snapshot{ConnectTimeoutMS: int(budget.ConnectTimeout() / time.Millisecond)}
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
