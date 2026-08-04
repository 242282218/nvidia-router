package nvidia

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
)

// MaxAudioResponseBytes bounds how much of a non-streaming audio response body
// we read before declaring a protocol error. Plain json transcripts are small;
// verbose_json carries per-word timestamps and gets a much larger budget below.
const MaxAudioResponseBytes = 8 << 10

// MaxVerboseAudioResponseBytes bounds verbose_json responses (word-level
// timestamps). The old 8 KiB cap rejected legitimate long transcripts and
// forced a wasteful key failover; 1 MiB covers realistic verbose payloads while
// still bounding memory.
const MaxVerboseAudioResponseBytes = 1 << 20

// AudioTranscriptions sends a non-streaming ASR request to NVIDIA as multipart
// form data. Like Chat, the per-attempt connect timeout comes from the
// request-scoped snapshot. The caller supplies a ready-to-send body (including
// the multipart boundary) plus the content-type header carrying that boundary.
func (c *Client) AudioTranscriptions(
	ctx context.Context,
	snapshot runtimeconfig.Snapshot,
	token string,
	body []byte,
	contentType string,
) (*http.Response, error) {
	replay, err := router.NewReplayableBody(body, os.TempDir())
	if err != nil {
		return nil, safeError{"capture NVIDIA audio transcriptions body", err}
	}
	defer func() { _ = replay.Close() }()
	return c.AudioTranscriptionsReplay(ctx, snapshot, token, replay, contentType)
}

func (c *Client) AudioTranscriptionsReplay(
	ctx context.Context,
	snapshot runtimeconfig.Snapshot,
	token string,
	body router.ReplayableBody,
	contentType string,
) (*http.Response, error) {
	if body == nil {
		return nil, safeError{"create NVIDIA audio transcriptions request", errors.New("request body is required")}
	}
	response, err := c.do(ctx, snapshot, func(ctx context.Context) (*http.Request, error) {
		request, err := c.descriptor.NewRequest(c.descriptor.ASR, false, token)
		if err != nil {
			return nil, safeError{"create NVIDIA audio transcriptions request", err}
		}
		reader, err := body.Open()
		if err != nil {
			return nil, safeError{"open NVIDIA audio transcriptions body", err}
		}
		request = request.WithContext(ctx)
		request.Body = reader
		request.GetBody = func() (io.ReadCloser, error) { return body.Open() }
		request.ContentLength = body.Size()
		request.Header.Set("Content-Type", contentType)
		return request, nil
	})
	if err != nil {
		return nil, safeError{"send NVIDIA audio transcriptions request", err}
	}
	return response, nil
}

// ValidatedAudioResponse carries the normalised transcript from a 2xx ASR
// response. The upstream request id is intentionally not captured here; no
// production consumer reads it.
type ValidatedAudioResponse struct {
	Body []byte
}

// ValidateNonstreamAudio reads and validates a non-streaming audio response. A
// 2xx body must be a JSON object containing `text` or `transcript`; anything
// else is surfaced as a protocol error so the Attempt loop can fail over.
// verboseJSON selects the larger read budget for verbose_json responses.
func ValidateNonstreamAudio(response *http.Response, verboseJSON bool) (ValidatedAudioResponse, error) {
	limit := int64(MaxAudioResponseBytes)
	if verboseJSON {
		limit = MaxVerboseAudioResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return ValidatedAudioResponse{}, safeError{"read NVIDIA audio response", err}
	}
	if len(body) > int(limit) {
		return ValidatedAudioResponse{}, protocolError()
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return ValidatedAudioResponse{}, protocolError()
	}
	if !nonEmptyStringField(fields, "text") && !nonEmptyStringField(fields, "transcript") {
		return ValidatedAudioResponse{}, protocolError()
	}

	return ValidatedAudioResponse{
		Body: body,
	}, nil
}

func nonEmptyStringField(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	if !ok {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

// AudioSpeech sends a streaming TTS request. The response body is left open so
// the caller can verify the first audio byte before committing downstream.
func (c *Client) AudioSpeech(
	ctx context.Context,
	snapshot runtimeconfig.Snapshot,
	token string,
	body []byte,
) (*http.Response, error) {
	response, err := c.do(ctx, snapshot, func(ctx context.Context) (*http.Request, error) {
		request, err := c.descriptor.NewRequest(c.descriptor.TTS, false, token)
		if err != nil {
			return nil, safeError{"create NVIDIA audio speech request", err}
		}
		request = request.WithContext(ctx)
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		request.ContentLength = int64(len(body))
		return request, nil
	})
	if err != nil {
		return nil, safeError{"send NVIDIA audio speech request", err}
	}
	return response, nil
}

// PrimeAudioSpeech proves that a successful upstream response has emitted audio
// before the Attempt orchestrator accepts the key. Buffered bytes remain in the
// response body and are copied downstream exactly once.
func PrimeAudioSpeech(ctx context.Context, response *http.Response) error {
	if response == nil || response.Body == nil {
		return protocolError()
	}
	reader := bufio.NewReader(response.Body)
	result := make(chan error, 1)
	go func() {
		_, err := reader.Peek(1)
		result <- err
	}()

	select {
	case err := <-result:
		if err == io.EOF {
			return protocolError()
		}
		if err != nil {
			return safeError{"read first NVIDIA audio speech byte", err}
		}
		response.Body = &bufferedAudioBody{Reader: reader, closer: response.Body}
		return nil
	case <-ctx.Done():
		_ = response.Body.Close()
		<-result
		return safeError{"wait for first NVIDIA audio speech byte", ctx.Err()}
	}
}

type bufferedAudioBody struct {
	*bufio.Reader
	closer io.Closer
}

func (b *bufferedAudioBody) Close() error {
	return b.closer.Close()
}
