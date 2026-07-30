package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterDoesNotRouteAPIPathsToFrontend(t *testing.T) {
	health := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	chat := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	responses := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	embeddings := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	audio := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	speech := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	models := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	router := NewRouter(health, chat, responses, embeddings, audio, speech, models)

	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/embeddings", "/v1/audio/transcriptions", "/v1/audio/speech", "/v1/models"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.Code)
		}
	}
}

func TestRouterFallbackRejectsUnknownV1Paths(t *testing.T) {
	router := NewRouter(http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler())

	for _, path := range []string{"/v1/some/unknown"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNotImplemented {
			t.Fatalf("%s status = %d, want 501", path, response.Code)
		}
	}
}

func TestRouterAdminAPIReturnsNotFound(t *testing.T) {
	router := NewRouter(http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("admin api status = %d, want 404", response.Code)
	}
}
