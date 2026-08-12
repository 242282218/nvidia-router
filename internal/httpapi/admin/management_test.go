package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestManagementRoutesProxyPoolStatusThroughMux locks in the fix for a routing
// regression: the mux registered only "/admin/api/proxy-pool" (no trailing
// slash), so the "/admin/api/proxy-pool/status" subpath the admin UI polls
// every 10 seconds fell through to http.NotFound in production.
func TestManagementRoutesProxyPoolStatusThroughMux(t *testing.T) {
	seen := make(map[string]int)
	proxyPool := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen[request.URL.Path]++
		writer.WriteHeader(http.StatusOK)
	})
	handler := NewManagement(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), // nvidia-keys
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), // access-keys
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), // models
		proxyPool,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), // audit-logs
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), // providers
	)

	for _, path := range []string{"/admin/api/proxy-pool", "/admin/api/proxy-pool/status"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, recorder.Code)
		}
	}
	if seen["/admin/api/proxy-pool/status"] != 1 {
		t.Fatalf("proxy-pool handler saw %v, want /admin/api/proxy-pool/status handled", seen)
	}
}
