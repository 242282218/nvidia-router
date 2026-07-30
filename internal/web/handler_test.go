package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesAssetsAndFallsBackToSPA(t *testing.T) {
	handler := newTestHandler(t)

	for _, path := range []string{"/", "/admin/keys", "/unknown/deep-link"} {
		t.Run(path, func(t *testing.T) {
			response := performRequest(handler, path)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if !strings.Contains(response.Body.String(), "embedded-spa") {
				t.Fatalf("body = %q, want embedded SPA index", response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html", got)
			}
		})
	}

	response := performRequest(handler, "/assets/app.js")
	if response.Code != http.StatusOK || response.Body.String() != "console.log('app')" {
		t.Fatalf("asset status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Fatalf("asset Content-Type = %q, want JavaScript", got)
	}
}

func TestHandlerNeverFallsBackForReservedAPIPrefixes(t *testing.T) {
	handler := newTestHandler(t)

	for _, path := range []string{"/v1/unknown", "/admin/api/unknown", "/health/unknown"} {
		t.Run(path, func(t *testing.T) {
			response := performRequest(handler, path)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
			}
			if strings.Contains(response.Body.String(), "embedded-spa") {
				t.Fatalf("reserved API path returned SPA: %q", response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestHandlerSetsStaticSecurityHeadersWithoutHSTS(t *testing.T) {
	handler := newTestHandler(t)
	response := performRequest(handler, "/login")

	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "img-src 'self' data:") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	if got := response.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("Strict-Transport-Security = %q, want absent over HTTP", got)
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	files := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>embedded-spa</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
	}
	handler, err := NewHandler(fs.FS(files))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler
}

func performRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
