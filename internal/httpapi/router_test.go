package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterDoesNotRouteAPIPathsToFrontend(t *testing.T) {
	ok := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	security := fakeAdminSecurity{}
	router := NewRouter(ok, ok, ok, ok, ok, ok, ok, security, http.NotFoundHandler(), ok, ok)

	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/embeddings", "/v1/audio/transcriptions", "/v1/audio/speech", "/v1/models"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.Code)
		}
		if response.Header().Get("X-Data-Guard") != "applied" {
			t.Fatalf("%s did not pass through the data guard", path)
		}
	}
}

func TestRouterFallbackRejectsUnknownV1Paths(t *testing.T) {
	notFound := http.NotFoundHandler()
	router := NewRouter(notFound, notFound, notFound, notFound, notFound, notFound, notFound, fakeAdminSecurity{}, notFound, notFound, notFound)

	for _, path := range []string{"/v1", "/v1/some/unknown"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNotImplemented {
			t.Fatalf("%s status = %d, want 501", path, response.Code)
		}
		if response.Header().Get("X-Data-Guard") != "applied" {
			t.Fatalf("%s did not pass through the data guard", path)
		}
	}
}

func TestRouterRegistersProtectedRuntimeAdministrationRoutes(t *testing.T) {
	notFound := http.NotFoundHandler()
	settings := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Settings-Method", request.Method)
		writer.WriteHeader(http.StatusOK)
	})
	runtimeSummary := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	router := NewRouter(
		notFound, notFound, notFound, notFound, notFound, notFound, notFound,
		fakeAdminSecurity{}, notFound, settings, runtimeSummary,
	)

	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/admin/api/settings"},
		{method: http.MethodPatch, path: "/admin/api/settings"},
		{method: http.MethodGet, path: "/admin/api/runtime/summary"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(request.method, request.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want 200", request.method, request.path, response.Code)
		}
		if response.Header().Get("X-Admin-Guard") != "applied" {
			t.Fatalf("%s %s did not pass through management guard", request.method, request.path)
		}
	}
}

func TestRouterSeparatesAuthAndProtectedAdminAPI(t *testing.T) {
	notFound := http.NotFoundHandler()
	router := NewRouter(notFound, notFound, notFound, notFound, notFound, notFound, notFound, fakeAdminSecurity{}, notFound, notFound, notFound)

	authResponse := httptest.NewRecorder()
	router.ServeHTTP(authResponse, httptest.NewRequest(http.MethodGet, "/admin/api/auth/session", nil))
	if authResponse.Code != http.StatusAccepted {
		t.Fatalf("auth API status = %d, want 202", authResponse.Code)
	}
	if authResponse.Header().Get("X-Admin-Guard") != "" {
		t.Fatal("auth API incorrectly passed through the general management guard")
	}

	managementResponse := httptest.NewRecorder()
	router.ServeHTTP(managementResponse, httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil))
	if managementResponse.Code != http.StatusNotFound {
		t.Fatalf("admin API status = %d, want 404", managementResponse.Code)
	}
	if managementResponse.Header().Get("X-Admin-Guard") != "applied" {
		t.Fatal("management API did not pass through the management guard")
	}
}

type fakeAdminSecurity struct{}

func (fakeAdminSecurity) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusAccepted)
}

func (fakeAdminSecurity) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Data-Guard", "applied")
		next.ServeHTTP(writer, request)
	})
}

func (fakeAdminSecurity) RequireManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Admin-Guard", "applied")
		next.ServeHTTP(writer, request)
	})
}
