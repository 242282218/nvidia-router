package adminauth

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateOriginRequiresSameOriginForUnsafeMethods(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		origin []string
		host   string
		tls    bool
		valid  bool
	}{
		{name: "same HTTP origin", method: http.MethodPost, origin: []string{"http://192.0.2.10:3756"}, host: "192.0.2.10:3756", valid: true},
		{name: "same HTTPS origin", method: http.MethodPatch, origin: []string{"https://admin.example.test"}, host: "admin.example.test", tls: true, valid: true},
		{name: "GET needs no Origin", method: http.MethodGet, host: "admin.example.test", valid: true},
		{name: "HEAD needs no Origin", method: http.MethodHead, host: "admin.example.test", valid: true},
		{name: "missing Origin", method: http.MethodDelete, host: "admin.example.test"},
		{name: "duplicate Origin", method: http.MethodPut, origin: []string{"http://admin.example.test", "http://admin.example.test"}, host: "admin.example.test"},
		{name: "cross origin", method: http.MethodPost, origin: []string{"http://other.example.test"}, host: "admin.example.test"},
		{name: "scheme mismatch", method: http.MethodPost, origin: []string{"https://admin.example.test"}, host: "admin.example.test"},
		{name: "userinfo", method: http.MethodPost, origin: []string{"http://user@admin.example.test"}, host: "admin.example.test"},
		{name: "path", method: http.MethodPost, origin: []string{"http://admin.example.test/path"}, host: "admin.example.test"},
		{name: "query", method: http.MethodPost, origin: []string{"http://admin.example.test?x=1"}, host: "admin.example.test"},
		{name: "fragment", method: http.MethodPost, origin: []string{"http://admin.example.test#fragment"}, host: "admin.example.test"},
		{name: "default port differs", method: http.MethodPost, origin: []string{"http://admin.example.test:80"}, host: "admin.example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "http://ignored.example.test/admin", nil)
			req.Host = test.host
			if test.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, origin := range test.origin {
				req.Header.Add("Origin", origin)
			}
			err := ValidateOrigin(req)
			if test.valid && err != nil {
				t.Fatalf("ValidateOrigin: %v", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidOrigin) {
				t.Fatalf("ValidateOrigin error = %v, want ErrInvalidOrigin", err)
			}
		})
	}
}

func TestOriginGuardRejectsInvalidUnsafeRequest(t *testing.T) {
	called := false
	handler := OriginGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "http://admin.example.test/admin", nil)
	req.Host = "admin.example.test"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)
	if called {
		t.Fatal("OriginGuard called the protected handler")
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf("OriginGuard status = %d, want 403", response.Code)
	}
}
