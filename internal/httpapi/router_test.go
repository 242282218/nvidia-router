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
	router := NewRouter(health, http.NotFoundHandler())

	for _, path := range []string{"/v1/chat/completions", "/admin/api/settings"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}
}
