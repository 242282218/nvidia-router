package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/config"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
	"nvidia-router/internal/upstream/nvidia"
)

func TestChatRejectsOversizedBody(t *testing.T) {
	handler := NewChat(nil, nil, nil)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(strings.Repeat("x", config.JSONBodyLimit+1)),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertChatError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestChatReturnsModelNotFound(t *testing.T) {
	handler := NewChat(modelResolverFunc(func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error) {
		return modelcatalog.Model{}, modelcatalog.ErrModelNotFound
	}), nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(false)))

	handler.ServeHTTP(response, request)

	assertChatError(t, response, http.StatusNotFound, "model_not_found")
}

func TestChatRejectsStreamingUntilSSEProxyIsAvailable(t *testing.T) {
	handler := NewChat(nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(true)))

	handler.ServeHTTP(response, request)

	assertChatError(t, response, http.StatusNotImplemented, "not_implemented")
}

func TestChatMapsRequestAndPreservesValidatedResponse(t *testing.T) {
	type capturedRequest struct {
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 1)
	responseBody := []byte(`{"id":"chat-1","choices":[],"future":{"kept":true}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured <- capturedRequest{header: request.Header.Clone(), body: body}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(responseBody)
	}))
	t.Cleanup(upstream.Close)

	descriptor := nvidia.DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := nvidia.NewClient(upstream.Client(), descriptor)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	lease := &releaseTrackingLease{id: 7}
	runner := attemptRunnerFunc(func(ctx context.Context, _ int64, _ bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
		response, err := execute(ctx, lease.id, []byte("upstream-secret"), &router.CommitState{})
		return router.AttemptResult{Response: response, Lease: lease, Attempts: 1}, err
	})
	resolver := modelResolverFunc(func(_ context.Context, publicID string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
		if publicID != "public-model" || requirements.Kind != modelcatalog.KindChat {
			t.Fatalf("resolve = %q, %#v", publicID, requirements)
		}
		return modelcatalog.Model{
			ID: 11, PublicID: publicID, UpstreamID: "vendor/model", Kind: modelcatalog.KindChat, Enabled: true,
		}, nil
	})
	handler := NewChat(resolver, runner, client)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatRequest(false)))

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), responseBody) {
		t.Fatalf("response = %d %s", response.Code, response.Body.Bytes())
	}
	if !lease.released {
		t.Fatal("successful attempt lease was not released")
	}
	got := <-captured
	if got.header.Get("Authorization") != "Bearer upstream-secret" {
		t.Fatalf("Authorization = %q", got.header.Get("Authorization"))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got.body, &fields); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if string(fields["model"]) != `"vendor/model"` || string(fields["future"]) != `{"kept":true}` {
		t.Fatalf("upstream fields = %s", got.body)
	}
}

func TestChatExecutionPreservesResponseReadFailureClassification(t *testing.T) {
	readErr := io.ErrUnexpectedEOF
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       readErrorBody{err: readErr},
		}, nil
	})}
	client, err := nvidia.NewClient(httpClient, nvidia.DefaultDescriptor())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := NewChat(nil, nil, client).execute([]byte(`{}`))(
		context.Background(), 1, []byte("upstream-secret"), &router.CommitState{},
	)
	if response != nil {
		defer response.Body.Close()
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("execute error = %v, want wrapped read error", err)
	}
	var classified fault.Fault
	if errors.As(err, &classified) && classified.PublicCode == "upstream_protocol_error" {
		t.Fatalf("read failure was misclassified as protocol error: %+v", classified)
	}
}

func validChatRequest(stream bool) string {
	return `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":` +
		map[bool]string{false: "false", true: "true"}[stream] + `,"future":{"kept":true}}`
}

func assertChatError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if payload.Error.Code != code {
		t.Fatalf("code = %q, want %q", payload.Error.Code, code)
	}
}

type modelResolverFunc func(context.Context, string, modelcatalog.Requirements) (modelcatalog.Model, error)

func (f modelResolverFunc) Resolve(ctx context.Context, id string, requirements modelcatalog.Requirements) (modelcatalog.Model, error) {
	return f(ctx, id, requirements)
}

type attemptRunnerFunc func(context.Context, int64, bool, router.ExecuteFunc) (router.AttemptResult, error)

func (f attemptRunnerFunc) Run(ctx context.Context, modelID int64, stream bool, execute router.ExecuteFunc) (router.AttemptResult, error) {
	return f(ctx, modelID, stream, execute)
}

type releaseTrackingLease struct {
	id       int64
	released bool
}

func (l *releaseTrackingLease) KeyID() int64 { return l.id }

func (l *releaseTrackingLease) Release() { l.released = true }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type readErrorBody struct{ err error }

func (b readErrorBody) Read([]byte) (int, error) { return 0, b.err }

func (readErrorBody) Close() error { return nil }
