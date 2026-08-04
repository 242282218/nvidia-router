package v1

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/observability"
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
	tempDir  string
	// bodyReadTimeout bounds reading the wire multipart body. Tests override it
	// to exercise the slow-upload path without waiting for the production value.
	bodyReadTimeout time.Duration
}

func NewAudio(models ModelResolver, attempts AttemptRunner, client *nvidia.Client, tempDirs ...string) *Audio {
	tempDir := os.TempDir()
	if len(tempDirs) > 0 && tempDirs[0] != "" {
		tempDir = tempDirs[0]
	}
	return &Audio{models: models, attempts: attempts, client: client, tempDir: tempDir, bodyReadTimeout: audioBodyReadTimeout}
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
	bodyLease, err := acquireBodyLease(request, audiocollections.MaxAudioBodyBytes)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer bodyLease.Release()
	originalCtx := request.Context()
	readCtx, cancel := context.WithTimeout(originalCtx, h.bodyReadTimeout)
	defer cancel()
	// ParseMultipart reads the wire body directly (not via readBoundedBody), so
	// a slow upload must be aborted by closing the body when the deadline fires.
	// http.Server has no ReadTimeout because SSE streams stay open, leaving this
	// close as the only backstop against a reader that holds the slot forever.
	stop := context.AfterFunc(readCtx, func() { _ = request.Body.Close() })
	defer stop()
	request = request.WithContext(readCtx)
	parsed, parseErr := audiocollections.ParseMultipart(request, h.tempDir)
	// The upload is fully read once parsing returns; release the read slot so a
	// long-lived upstream call cannot monopolize the body-read budget, and drop
	// the upload deadline so it cannot eat into the attempt's first-byte budget.
	bodyLease.releaseSlot()
	request = request.WithContext(originalCtx)
	if parseErr == nil {
		defer func() { _ = parsed.Close() }()
	}
	if err := removeMultipartFiles(request); err != nil {
		if parseErr == nil {
			_ = parsed.Close()
		}
		writeChatError(writer, &apierror.Error{
			Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
			Message: "The server could not clean up the audio upload.",
		})
		return
	}
	if parseErr != nil {
		if errors.Is(readCtx.Err(), context.DeadlineExceeded) {
			writeChatError(writer, &apierror.Error{
				Status: http.StatusRequestTimeout, Type: "invalid_request_error", Code: "request_timeout",
				Message: "The audio upload took too long to complete.",
			})
			return
		}
		writeChatError(writer, parseErr)
		return
	}
	if h.models == nil {
		writeChatError(writer, &apierror.Error{Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error", Message: "The audio service is not configured."})
		return
	}
	observability.SetModel(request.Context(), parsed.ModelID(), false)
	model, err := h.models.Resolve(request.Context(), parsed.ModelID(), parsed.Requirements())
	if err != nil {
		writeChatError(writer, modelError(err))
		return
	}
	if h.attempts == nil || h.client == nil {
		writeChatError(writer, &apierror.Error{Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error", Message: "The audio service is not configured."})
		return
	}
	upstreamBody, contentType, err := parsed.EncodeUpstream(model.UpstreamID)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	defer func() { _ = upstreamBody.Close() }()
	result, err := h.attempts.Run(request.Context(), model.ID, false, h.execute(upstreamBody, contentType, parsed.ResponseFormat()))
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
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(writer, result.Response.Body)
}

func (h *Audio) execute(body router.ReplayableBody, contentType, responseFormat string) router.ExecuteFunc {
	return func(ctx context.Context, _ int64, secret []byte, _ *router.CommitState) (*http.Response, error) {
		response, err := h.client.AudioTranscriptionsReplay(ctx, snapshotFromBudget(ctx), string(secret), body, contentType)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return response, nil
		}
		validated, err := nvidia.ValidateNonstreamAudio(response, responseFormat == "verbose_json")
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
	payload, bodyLease, err := readBodyWithLease(request, bodyReadLimitForJSON(), jsonBodyReadTimeout)
	if bodyLease != nil {
		defer bodyLease.Release()
	}
	if err != nil {
		writeChatError(writer, err)
		return
	}
	parsed, err := audiocollections.ParseSpeech(payload)
	if err != nil {
		writeChatError(writer, err)
		return
	}
	observability.SetModel(request.Context(), parsed.PublicModelID(), true)
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
	defer func() { _ = result.Response.Body.Close() }()

	commit := result.Commit
	if commit == nil {
		commit = &router.CommitState{}
	}
	output := commit.Wrap(writer)
	output.Header().Set("Content-Type", safeAudioContentType(result.Response.Header.Get("Content-Type")))
	// Audio speech streams audio bytes; without this header the default nginx
	// `proxy_buffering on` setting would batch the audio clip until the buffer
	// fills, delaying the first byte of audio clients hear (audit B6). Non-nginx
	// proxies ignore the header.
	output.Header().Set("X-Accel-Buffering", "no")
	output.WriteHeader(result.Response.StatusCode)
	buffer := make([]byte, 32<<10)
	if _, err := io.CopyBuffer(output, result.Response.Body, buffer); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, nvidia.ErrAudioStreamIdle) {
			observability.SetErrorCode(request.Context(), "audio_stream_interrupted")
		}
	}
}

func (h *Speech) execute(body []byte) router.ExecuteFunc {
	return func(ctx context.Context, _ int64, secret []byte, commit *router.CommitState) (*http.Response, error) {
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
		if budget, ok := router.BudgetFromContext(ctx); ok {
			response.Body = nvidia.WithAudioIdleTimeout(response.Body, budget.FirstByteTimeout())
		}
		commit.Commit()
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
