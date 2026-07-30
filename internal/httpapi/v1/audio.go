package v1

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/fault"
	audiocollections "nvidia-router/internal/protocol/audio"
	"nvidia-router/internal/router"
	"nvidia-router/internal/upstream/nvidia"
)

// Audio proxies OpenAI Audio Transcriptions requests through the same
// ModelCatalog, Pool, Attempt orchestrator and NVIDIA Client as the other
// handlers. It is non-streaming and relies on the Attempt loop for first-byte
// failover: a 503 before the first byte retries the next key, but once bytes
// reach the client the key is locked in.
type Audio struct {
	models   ModelResolver
	attempts AttemptRunner
	client   *nvidia.Client
}

func NewAudio(models ModelResolver, attempts AttemptRunner, client *nvidia.Client) *Audio {
	return &Audio{models: models, attempts: attempts, client: client}
}

func (h *Audio) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusMethodNotAllowed, Type: "invalid_request_error", Code: "method_not_allowed",
			Message: "Only POST is supported for this endpoint.",
		})
		return
	}
	if err := verifyMultipartAudio(request); err != nil {
		writeChatError(writer, err)
		return
	}
	parsed, parseErr := audiocollections.ParseMultipart(request)
	if err := removeMultipartFiles(request); err != nil {
		writeChatError(writer, &apierror.Error{
			Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
			Message: "The server could not clean up the audio upload.",
		})
		return
	}
	if parseErr != nil {
		writeChatError(writer, parseErr)
		return
	}
	model, err := h.models.Resolve(request.Context(), parsed.ModelID(), parsed.Requirements())
	if err != nil {
		writeChatError(writer, modelError(err))
		return
	}
	upstreamBody, contentType, err := parsed.EncodeUpstream(model.UpstreamID)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	result, err := h.attempts.Run(request.Context(), model.ID, false, h.execute(upstreamBody, contentType))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer result.Response.Body.Close()

	if result.Response.StatusCode < http.StatusOK || result.Response.StatusCode >= http.StatusMultipleChoices {
		writeChatError(writer, &apierror.Error{
			Status: result.Response.StatusCode, Type: "upstream_error", Code: "upstream_error",
			Message: "The upstream service returned an error.",
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(writer, result.Response.Body)
}

func (h *Audio) execute(body []byte, contentType string) router.ExecuteFunc {
	return func(ctx context.Context, _ int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		response, err := h.client.AudioTranscriptions(ctx, snapshotFromBudget(ctx), string(secret), body, contentType)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return response, nil
		}
		validated, err := nvidia.ValidateNonstreamAudio(response)
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

func removeMultipartFiles(request *http.Request) error {
	if request.MultipartForm == nil {
		return nil
	}
	if err := request.MultipartForm.RemoveAll(); err != nil {
		return fmt.Errorf("remove multipart temporary files: %w", err)
	}
	return nil
}

// verifyMultipartAudio rejects non-multipart content types before parsing so
// the client receives a clean 400 rather than a parse failure.
func verifyMultipartAudio(request *http.Request) error {
	contentType := request.Header.Get("Content-Type")
	if contentType == "" {
		return &apierror.Error{
			Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_audio",
			Message: "The request must use multipart/form-data.",
		}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		return &apierror.Error{
			Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_audio",
			Message: "The request must use multipart/form-data.",
		}
	}
	return nil
}

// Speech proxies OpenAI Audio Speech requests as a binary stream. It does not
// commit downstream headers until the upstream body has yielded audio.
type Speech struct {
	models   ModelResolver
	attempts AttemptRunner
	client   *nvidia.Client
}

func NewSpeech(models ModelResolver, attempts AttemptRunner, client *nvidia.Client) *Speech {
	return &Speech{models: models, attempts: attempts, client: client}
}

func (h *Speech) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
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
	parsed, err := audiocollections.ParseSpeech(payload)
	if err != nil {
		writeChatError(writer, err)
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
	result, err := h.attempts.Run(request.Context(), model.ID, true, h.execute(upstreamBody))
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer result.Release()
	defer result.Response.Body.Close()

	writer.Header().Set("Content-Type", safeAudioContentType(result.Response.Header.Get("Content-Type")))
	writer.WriteHeader(result.Response.StatusCode)
	buffer := make([]byte, 32<<10)
	_, _ = io.CopyBuffer(writer, result.Response.Body, buffer)
}

func (h *Speech) execute(body []byte) router.ExecuteFunc {
	return func(ctx context.Context, _ int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		response, err := h.client.AudioSpeech(ctx, snapshotFromBudget(ctx), string(secret), body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return response, nil
		}
		primeCtx := ctx
		cancel := func() {}
		if budget, ok := router.BudgetFromContext(ctx); ok {
			primeCtx, cancel = context.WithDeadline(ctx, budget.FirstByteDeadline())
		}
		defer cancel()
		if err := nvidia.PrimeAudioSpeech(primeCtx, response); err != nil {
			if errors.Is(err, nvidia.ErrProtocol) {
				return response, fault.Protocol(err)
			}
			return response, err
		}
		return response, nil
	}
}

func safeAudioContentType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "application/octet-stream"
	}
	switch mediaType {
	case "audio/wav", "audio/mpeg", "audio/ogg", "audio/flac", "application/octet-stream":
		return mediaType
	default:
		return "application/octet-stream"
	}
}
