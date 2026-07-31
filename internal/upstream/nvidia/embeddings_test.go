package nvidia

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/runtimeconfig"
)

func TestEmbeddingsSendsWireFormat(t *testing.T) {
	var captured struct {
		method, auth, contentType, accept string
		body                              string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured.method = request.Method
		captured.auth = request.Header.Get("Authorization")
		captured.contentType = request.Header.Get("Content-Type")
		captured.accept = request.Header.Get("Accept")
		body, _ := io.ReadAll(request.Body)
		captured.body = string(body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":3}}`))
	}))
	t.Cleanup(upstream.Close)

	descriptor := DefaultDescriptor()
	descriptor.Embedding.URL = upstream.URL + "/v1/embeddings"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	response, err := client.Embeddings(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 10000}, "nvapi-secret", []byte(`{"model":"vendor/embed","input":"hi"}`))
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if captured.method != http.MethodPost {
		t.Fatalf("method = %q", captured.method)
	}
	if captured.auth != "Bearer nvapi-secret" {
		t.Fatalf("auth = %q", captured.auth)
	}
	if captured.contentType != "application/json" {
		t.Fatalf("content-type = %q", captured.contentType)
	}
	// Embeddings must not request the SSE accept header.
	if captured.accept != "" {
		t.Fatalf("accept = %q, want empty", captured.accept)
	}
	if captured.body != `{"model":"vendor/embed","input":"hi"}` {
		t.Fatalf("body = %q", captured.body)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestValidateNonstreamEmbeddingsRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"not json":       `hello`,
		"no data":        `{"usage":{}}`,
		"data not array": `{"data":{"x":1}}`,
		"empty body":     `{}`,
	}
	for name, body := range cases {
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		if _, err := ValidateNonstreamEmbeddings(response); err == nil {
			t.Fatalf("%s: expected protocol error", name)
		}
	}
}

func TestValidateNonstreamEmbeddingsAcceptsValid(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"req-1"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1]}],"usage":{"prompt_tokens":1}}`)),
	}
	validated, err := ValidateNonstreamEmbeddings(response)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Metadata.RequestID != "req-1" {
		t.Fatalf("request id = %q", validated.Metadata.RequestID)
	}
	if len(validated.Body) == 0 {
		t.Fatal("body was not preserved")
	}
}
