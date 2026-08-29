package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/observability"
	responsesprotocol "nvidia-router/internal/protocol/responses"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
)

func (h *Responses) serveOpenCodeFreeResponses(writer http.ResponseWriter, request *http.Request, body []byte, responseID string, model modelcatalog.Model, config responsesprotocol.ResponseConfig, stream bool) {
	if h.openCodeFree == nil {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusServiceUnavailable, Type: "server_error", Code: "provider_unconfigured",
			Message: "The OpenCodeFree gateway is not configured.",
		})
		return
	}

	tracked := &firstWriteTracker{ResponseWriter: writer}
	execution := openCodeFreeExecution{
		call: func(ctx context.Context, stream bool) (*http.Response, error) {
			return h.openCodeFree.Chat(ctx, runtimeconfig.Snapshot{}, body, stream)
		},
	}
	err := execution.run(nvidia.WithForwardedHeaders(request.Context(), request.Header), stream, tracked,
		func(ctx context.Context, response *http.Response) error {
			if stream {
				h.streamResponseWithConfig(ctx, tracked, response, responseID, model.PublicID, config)
				return nil
			}

			validated, validateErr := nvidia.ValidateNonstreamChat(response)
			if validateErr != nil {
				if errors.Is(validateErr, nvidia.ErrEmptyResponse) {
					return fault.EmptyResponse(validateErr)
				}
				if errors.Is(validateErr, nvidia.ErrProtocol) {
					return fault.Protocol(validateErr)
				}
				return validateErr
			}
			if present, chars := observability.ReasoningContentFromBody(validated.Body); present {
				observability.SetReasoningResponse(ctx, present, chars)
				if observability.ReasoningStarvedFromBody(validated.Body) {
					return fault.EmptyResponse(errors.New("reasoning consumed completion budget"))
				}
			}
			converted, convertErr := responsesprotocol.FromChatWithConfig(validated.Body, responseID, model, config)
			if convertErr != nil {
				// Conversion is a local protocol failure after a valid upstream
				// body; preserve its public fault without replaying the request.
				return openCodeFreeNonRetryable{err: fault.Protocol(fmt.Errorf("convert OpenCodeFree chat response: %w", convertErr))}
			}
			markResponseComplete(response)
			copyResponseHeaders(tracked.Header(), response.Header)
			tracked.Header().Set("Content-Type", "application/json")
			tracked.WriteHeader(http.StatusOK)
			_, _ = tracked.Write(converted)
			return nil
		})
	if err != nil {
		writeChatError(writer, fmt.Errorf("OpenCodeFree Responses: %w", err))
	}
}
