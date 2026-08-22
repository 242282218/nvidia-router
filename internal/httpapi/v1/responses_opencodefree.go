package v1

import (
	"context"
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

	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(nvidia.WithForwardedHeaders(request.Context(), request.Header), openCodeFreeRequestTimeout)
		response, err := h.openCodeFree.Chat(ctx, runtimeconfig.Snapshot{}, body, stream)
		if err != nil {
			cancel()
			writeChatError(writer, fmt.Errorf("OpenCodeFree Responses: %w", err))
			return
		}
		if response == nil || response.Body == nil {
			cancel()
			writeChatError(writer, fault.EmptyResponse(errors.New("OpenCodeFree Responses returned no response")))
			return
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			retry, mapped := openCodeFreeResponsesStatus(response, attempt == 0, stream)
			cancel()
			if retry {
				select {
				case <-time.After(500 * time.Millisecond):
					continue
				case <-request.Context().Done():
					return
				}
			}
			writeChatError(writer, mapped)
			return
		}
		if stream {
			h.streamResponseWithConfig(ctx, writer, response, responseID, model.PublicID, config)
			_ = response.Body.Close()
			cancel()
			return
		}

		validated, validateErr := nvidia.ValidateNonstreamChat(response)
		_ = response.Body.Close()
		if validateErr != nil {
			cancel()
			if attempt == 0 && (errors.Is(validateErr, nvidia.ErrEmptyResponse) || errors.Is(validateErr, nvidia.ErrProtocol)) {
				select {
				case <-time.After(500 * time.Millisecond):
					continue
				case <-request.Context().Done():
					return
				}
			}
			if errors.Is(validateErr, nvidia.ErrEmptyResponse) {
				writeChatError(writer, fault.EmptyResponse(validateErr))
				return
			}
			if errors.Is(validateErr, nvidia.ErrProtocol) {
				writeChatError(writer, fault.Protocol(validateErr))
				return
			}
			writeChatError(writer, validateErr)
			return
		}
		if present, chars := observability.ReasoningContentFromBody(validated.Body); present {
			observability.SetReasoningResponse(request.Context(), present, chars)
		}
		converted, convertErr := responsesprotocol.FromChatWithConfig(validated.Body, responseID, model, config)
		cancel()
		if convertErr != nil {
			writeChatError(writer, fault.Protocol(fmt.Errorf("convert OpenCodeFree chat response: %w", convertErr)))
			return
		}
		markResponseComplete(response)
		copyResponseHeaders(writer.Header(), response.Header)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(converted)
		return
	}
}

func openCodeFreeResponsesStatus(response *http.Response, allowRetry, stream bool) (bool, error) {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	_ = response.Body.Close()
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = fmt.Sprintf("OpenCodeFree upstream returned HTTP %d", response.StatusCode)
	} else if len(message) > 512 {
		message = message[:512]
	}
	if response.StatusCode == http.StatusNotFound {
		return false, &apierror.Error{Status: http.StatusBadGateway, Type: "server_error", Code: "upstream_model_not_found", Message: message}
	}
	if response.StatusCode == http.StatusTooManyRequests || isOpenCodeFreeTransientStatus(response.StatusCode) {
		retry := allowRetry && !stream && response.StatusCode != http.StatusTooManyRequests
		if retry {
			return true, nil
		}
		status := response.StatusCode
		if status == 436 {
			status = http.StatusBadGateway
		}
		return false, fault.New(status, fault.ScopeUpstreamGlobal, "server_error", "upstream_unavailable", message, nil)
	}
	return false, &apierror.Error{Status: http.StatusBadGateway, Type: "server_error", Code: "upstream_error", Message: message}
}
