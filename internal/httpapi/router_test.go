package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "nvidia-router/internal/httpapi/v1"
)

func TestRouterDoesNotRouteAPIPathsToFrontend(t *testing.T) {
	ok := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	security := fakeAdminSecurity{}
	router := NewRouter(ok, ok, ok, ok, ok, ok, ok, v1.Unsupported, security, http.NotFoundHandler(), ok, ok, http.NotFoundHandler())

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
	router := NewRouter(notFound, notFound, notFound, notFound, notFound, notFound, notFound, v1.Unsupported, fakeAdminSecurity{}, notFound, notFound, notFound, notFound)

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

func TestNewRouterPanicsWhenUnsupportedHandlerIsNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRouter did not panic for nil unsupported handler")
		}
	}()

	notFound := http.NotFoundHandler()
	NewRouter(notFound, notFound, notFound, notFound, notFound, notFound, notFound, nil, fakeAdminSecurity{}, notFound, notFound, notFound, notFound)
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
		notFound, notFound, notFound, notFound, notFound, notFound, notFound, v1.Unsupported,
		fakeAdminSecurity{}, notFound, settings, runtimeSummary, notFound,
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

func TestRouterRegistersProtectedMonitoringRoutes(t *testing.T) {
	notFound := http.NotFoundHandler()
	monitoring := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Monitoring-Handler", "applied")
		writer.WriteHeader(http.StatusOK)
	})
	router := NewRouter(
		notFound, notFound, notFound, notFound, notFound, notFound, notFound, v1.Unsupported,
		fakeAdminSecurity{}, notFound, notFound, notFound, notFound, notFound, monitoring,
	)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/monitoring/summary", nil))
	if response.Code != http.StatusOK || response.Header().Get("X-Monitoring-Handler") != "applied" {
		t.Fatalf("monitoring route = %d/%q, want 200/applied", response.Code, response.Header().Get("X-Monitoring-Handler"))
	}
	if response.Header().Get("X-Admin-Guard") != "applied" {
		t.Fatal("monitoring route did not pass through management guard")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("monitoring Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func TestRouterAddsNoStoreToAPIHealthAndAuthResponsesWithoutSPAHTML(t *testing.T) {
	api := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"ok":true}`))
	})
	frontend := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("SPA INDEX MARKER"))
	})
	router := NewRouter(api, api, api, api, api, api, api, v1.Unsupported, fakeAdminSecurity{}, api, api, api, frontend)

	for _, path := range []string{"/v1/models", "/admin/api/auth/session", "/admin/api/settings", "/health/live"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", path, got)
		}
		if response.Header().Get("Content-Type") == "text/html; charset=utf-8" {
			t.Errorf("%s returned SPA HTML: %q", path, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	if response.Body.String() != "SPA INDEX MARKER" {
		t.Fatalf("frontend body = %q, want SPA marker", response.Body.String())
	}
}

func TestNoStoreMiddlewareAppendsWithoutReplacingCacheDirectives(t *testing.T) {
	handler := NoStoreMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "private, max-age=0")
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if got := response.Header().Get("Cache-Control"); got != "private, max-age=0, no-store" {
		t.Fatalf("Cache-Control = %q, want existing directives plus no-store", got)
	}
}
func TestRouterSeparatesAuthAndProtectedAdminAPI(t *testing.T) {
	notFound := http.NotFoundHandler()

	router := NewRouter(notFound, notFound, notFound, notFound, notFound, notFound, notFound, v1.Unsupported, fakeAdminSecurity{}, notFound, notFound, notFound, notFound)

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

// Metrics expose pool size, cooldown counts and request outcomes: enough for an
// unauthenticated observer to tell when the key or proxy pool is exhausted.
func TestRouterProtectsMetrics(t *testing.T) {
	notFound := http.NotFoundHandler()
	metrics := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("nvidia_router_proxy_pool_healthy 3\n"))
	})
	router := NewRouter(
		notFound, notFound, notFound, notFound, notFound, notFound, notFound, v1.Unsupported,
		fakeAdminSecurity{}, notFound, notFound, notFound, notFound,
		notFound, notFound, metrics,
	)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	// Assert the metrics handler itself ran: the guard sets its header even when
	// it fronts a NotFound handler, so the header alone proves nothing.
	if !strings.Contains(response.Body.String(), "nvidia_router_proxy_pool_healthy") {
		t.Fatalf("metrics handler not wired, body = %q", response.Body.String())
	}
	if response.Header().Get("X-Admin-Guard") != "applied" {
		t.Fatal("/metrics bypassed the management guard")
	}
}
