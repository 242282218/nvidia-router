package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnsupportedPathsRejectWithNotImplemented(t *testing.T) {
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodPost, "/v1/audio/transcriptions"},
		{http.MethodPost, "/v1/audio/speech"},
		{http.MethodPost, "/v1/anything-unknown"},
		{http.MethodGet, "/v1/some/future/endpoint"},
	}
	for _, target := range paths {
		t.Run(target.method+" "+target.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			Unsupported.ServeHTTP(response, httptest.NewRequest(target.method, target.path, nil))

			if response.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body = %s", response.Code, response.Body.String())
			}
			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode error: %v; body=%s", err, response.Body.String())
			}
			if payload.Error.Code != "not_implemented" {
				t.Fatalf("code = %q, want not_implemented", payload.Error.Code)
			}
			if payload.Error.Message == "" {
				t.Fatal("expected non-empty public message")
			}
			if strings.Contains(response.Body.String(), "nvapi") {
				t.Fatalf("response leaks secret fragment: %s", response.Body.String())
			}
		})
	}
}
