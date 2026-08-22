package v1

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/runtimeconfig"
)

type responsesTestModels struct {
	model modelcatalog.Model
}

func (m responsesTestModels) Resolve(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
	return m.model, nil
}

type responsesTestOpenCodeFree struct {
	body []byte
}

func (c responsesTestOpenCodeFree) Chat(context.Context, runtimeconfig.Snapshot, []byte, bool) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(c.body))),
	}, nil
}

func TestResponsesRoutesOpenCodeFreeModel(t *testing.T) {
	model := modelcatalog.Model{
		ID:         1,
		PublicID:   "opencodefree/test-model",
		UpstreamID: "test-model",
		Kind:       modelcatalog.KindChat,
		Provider:   modelcatalog.ProviderOpenCodeFree,
		Enabled:    true,
	}
	chatBody := []byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	handler := NewResponses(responsesTestModels{model: model}, nil, nil).WithOpenCodeFree(responsesTestOpenCodeFree{body: chatBody})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"opencodefree/test-model","input":"hi","store":false}`))
	writer := httptest.NewRecorder()

	handler.ServeHTTP(writer, request)
	if writer.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", writer.Code, writer.Body.String())
	}
	if !strings.Contains(writer.Body.String(), `"type":"message"`) {
		t.Fatalf("response is not a Responses message: %s", writer.Body.String())
	}
}
