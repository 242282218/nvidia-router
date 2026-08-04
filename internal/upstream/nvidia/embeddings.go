package nvidia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"nvidia-router/internal/runtimeconfig"
)

// MaxEmbeddingsResponseBytes bounds how much of a non-streaming embeddings
// response body we read before declaring a protocol error.
const MaxEmbeddingsResponseBytes = 32 << 20

// Embeddings sends a non-streaming embeddings request to NVIDIA. Like Chat,
// the per-attempt connect timeout comes from the request-scoped snapshot.
func (c *Client) Embeddings(
	ctx context.Context,
	snapshot runtimeconfig.Snapshot,
	token string,
	body []byte,
) (*http.Response, error) {
	response, err := c.do(ctx, snapshot, func(ctx context.Context) (*http.Request, error) {
		request, err := c.descriptor.NewRequest(c.descriptor.Embedding, false, token)
		if err != nil {
			return nil, safeError{"create NVIDIA embeddings request", err}
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
		return nil, safeError{"send NVIDIA embeddings request", err}
	}
	return response, nil
}

// ValidatedEmbeddingsResponse is the validated body of a 2xx non-streaming
// embeddings response. The upstream request id and usage payload are not
// captured: no production consumer reads them, and the streaming usage path is
// already handled separately in the SSE proxy layer.
type ValidatedEmbeddingsResponse struct {
	Body []byte
}

// ValidateNonstreamEmbeddings reads and validates a non-streaming embeddings
// response. A 2xx body must be a JSON object containing a `data` array; anything
// else is surfaced as a protocol error so the Attempt loop can fail over.
func ValidateNonstreamEmbeddings(response *http.Response) (ValidatedEmbeddingsResponse, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxEmbeddingsResponseBytes+1))
	if err != nil {
		return ValidatedEmbeddingsResponse{}, safeError{"read NVIDIA embeddings response", err}
	}
	if len(body) > MaxEmbeddingsResponseBytes {
		return ValidatedEmbeddingsResponse{}, protocolError()
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return ValidatedEmbeddingsResponse{}, protocolError()
	}
	data, exists := fields["data"]
	if !exists || !isJSONArray(data) {
		return ValidatedEmbeddingsResponse{}, protocolError()
	}

	return ValidatedEmbeddingsResponse{
		Body: body,
	}, nil
}
