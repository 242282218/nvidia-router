package nvidia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func TestChatSendsGoldenRequest(t *testing.T) {
	type capturedRequest struct {
		method string
		path   string
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured <- capturedRequest{
			method: request.Method,
			path:   request.URL.Path,
			header: request.Header.Clone(),
			body:   body,
		}
		writer.Header().Set("X-Request-Id", "request-123")
		_, _ = writer.Write([]byte(`{"id":"chat-1","choices":[]}`))
	}))
	t.Cleanup(server.Close)

	body := goldenChatBody(t)
	client := newChatTestClient(t, server.URL, server.Client())
	response, err := client.Chat(
		context.Background(),
		runtimeconfig.Snapshot{ConnectTimeoutMS: 250},
		"test-token",
		body,
		true,
	)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	_ = response.Body.Close()

	got := <-captured
	if got.method != http.MethodPost || got.path != "/v1/chat/completions" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
	if authorization := got.header.Get("Authorization"); authorization != "Bearer test-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if contentType := got.header.Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if accept := got.header.Get("Accept"); accept != "text/event-stream" {
		t.Fatalf("Accept = %q", accept)
	}
	assertGoldenChatBody(t, got.body)
}

func TestChatCreatesAttemptScopedTransport(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport)
	firstTransport, firstDialer := newAttemptTransport(base, runtimeconfig.Snapshot{ConnectTimeoutMS: 125})
	secondTransport, secondDialer := newAttemptTransport(base, runtimeconfig.Snapshot{ConnectTimeoutMS: 875})

	if firstTransport == secondTransport || firstTransport == base || secondTransport == base {
		t.Fatal("attempt transports were shared")
	}
	if firstDialer == nil || firstDialer.Timeout != 125*time.Millisecond {
		t.Fatalf("first dialer = %#v", firstDialer)
	}
	if secondDialer == nil || secondDialer.Timeout != 875*time.Millisecond {
		t.Fatalf("second dialer = %#v", secondDialer)
	}
	if firstTransport.(*http.Transport).DialContext == nil || secondTransport.(*http.Transport).DialContext == nil {
		t.Fatal("attempt transport is missing DialContext")
	}
}

func TestChatErrorDoesNotExposeURLOrKey(t *testing.T) {
	const (
		endpoint = "https://private-upstream.example/v1/chat/completions"
		secret   = "nvapi-secret-value"
	)
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(endpoint + " rejected " + secret)
	})}
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = endpoint
	client, err := NewClient(httpClient, descriptor, fixedSettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 100}, secret, []byte(`{}`), false)
	if err == nil {
		t.Fatal("Chat succeeded")
	}
	if strings.Contains(err.Error(), endpoint) || strings.Contains(err.Error(), secret) {
		t.Fatalf("Chat error exposed URL or key: %v", err)
	}
}

func TestValidateNonstreamChatPreservesBodyAndExtractsMetadata(t *testing.T) {
	body := []byte(`{"id":"chat-1","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":4},"future":{"x":1}}`)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"request-123"}, "X-Secret": []string{"hidden"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}

	validated, err := ValidateNonstreamChat(response)
	if err != nil {
		t.Fatalf("ValidateNonstreamChat: %v", err)
	}
	if string(validated.Body) != string(body) {
		t.Fatalf("body = %s", validated.Body)
	}
	if validated.Metadata.RequestID != "request-123" {
		t.Fatalf("request ID = %q", validated.Metadata.RequestID)
	}
	if string(validated.Metadata.Usage) != `{"prompt_tokens":3,"completion_tokens":4}` {
		t.Fatalf("usage = %s", validated.Metadata.Usage)
	}
}

func TestValidateNonstreamChatRejectsMalformedSuccess(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "non JSON", body: "not-json private-body"},
		{name: "array", body: `[]`},
		{name: "missing choices", body: `{"id":"chat-1"}`},
		{name: "empty choices", body: `{"choices":[]}`},
		{name: "null first choice", body: `{"choices":[null]}`},
		{name: "null choices", body: `{"choices":null}`},
		{name: "non-array choices", body: `{"choices":"not-array"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(test.body)),
			}
			_, err := ValidateNonstreamChat(response)
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("error = %v, want ErrProtocol", err)
			}
			if strings.Contains(err.Error(), test.body) || strings.Contains(err.Error(), "private-body") {
				t.Fatalf("protocol error exposed body: %v", err)
			}
		})
	}
}

func TestValidateNonstreamChatEnforcesResponseLimit(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", MaxChatResponseBytes+1))),
	}
	_, err := ValidateNonstreamChat(response)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("error = %v, want ErrProtocol", err)
	}
}

func TestValidateNonstreamChatHidesBodyReadError(t *testing.T) {
	readErr := errors.New("read failed with nvapi-secret-value")
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       errorReadCloser{err: readErr},
	}
	_, err := ValidateNonstreamChat(response)
	if !errors.Is(err, readErr) {
		t.Fatalf("error = %v, want wrapped read error", err)
	}
	if strings.Contains(err.Error(), "nvapi-secret-value") {
		t.Fatalf("read error exposed secret: %v", err)
	}
}

func goldenChatBody(t *testing.T) []byte {
	t.Helper()
	return []byte(`{
		"model":"vendor/model",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true,
		"tools":[{"type":"function","function":{"name":"lookup"}}],
		"reasoning_effort":"medium",
		"future_flag":{"kept":true}
	}`)
}

func assertGoldenChatBody(t *testing.T, body []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if string(fields["model"]) != `"vendor/model"` || string(fields["stream"]) != "true" {
		t.Fatalf("route fields = model:%s stream:%s", fields["model"], fields["stream"])
	}
	if string(fields["reasoning_effort"]) != `"medium"` {
		t.Fatalf("reasoning_effort = %s", fields["reasoning_effort"])
	}
	if _, exists := fields["thinking"]; exists {
		t.Fatal("thinking was not normalized")
	}
	if len(fields["tools"]) == 0 || string(fields["future_flag"]) != `{"kept":true}` {
		t.Fatalf("tools/future = %s / %s", fields["tools"], fields["future_flag"])
	}
}

func newChatTestClient(t *testing.T, baseURL string, httpClient *http.Client) *Client {
	t.Helper()
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = baseURL + "/v1/chat/completions"
	client, err := NewClient(httpClient, descriptor, fixedSettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}
