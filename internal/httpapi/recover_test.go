package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testRecoverLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecoverMiddlewareWritesJSON500OnPanic(t *testing.T) {
	handler := RecoverMiddleware(testRecoverLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		var values []string
		_ = values[0]
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Error.Code != "internal_error" {
		t.Fatalf("error code = %q, want internal_error", body.Error.Code)
	}
}

func TestRecoverMiddlewareDoesNotRewriteCommittedResponse(t *testing.T) {
	handler := RecoverMiddleware(testRecoverLogger(), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "partial")
		panic("stream exploded")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want committed 200 preserved", response.Code)
	}
	if body := response.Body.String(); body != "partial" {
		t.Fatalf("body = %q, want committed bytes untouched", body)
	}
}

func TestRecoverMiddlewareRePanicsErrAbortHandler(t *testing.T) {
	handler := RecoverMiddleware(testRecoverLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("recovered = %v, want http.ErrAbortHandler re-panicked", recovered)
		}
	}()
	handler.ServeHTTP(response, request)
	t.Fatal("expected ErrAbortHandler to propagate")
}

func TestRecoverMiddlewarePassesThroughSuccess(t *testing.T) {
	handler := RecoverMiddleware(testRecoverLogger(), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "ok" {
		t.Fatalf("status/body = %d/%q, want 200/ok", response.Code, response.Body.String())
	}
}
