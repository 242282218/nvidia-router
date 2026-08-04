package adminauth

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{name: "case insensitive scheme", method: http.MethodPost, origin: []string{"HTTP://ADMIN.EXAMPLE.TEST"}, host: "admin.example.test", valid: true},
		{name: "GET needs no Origin", method: http.MethodGet, host: "admin.example.test", valid: true},
		{name: "HEAD needs no Origin", method: http.MethodHead, host: "admin.example.test", valid: true},
		{name: "missing Origin", method: http.MethodDelete, host: "admin.example.test"},
		{name: "empty Origin", method: http.MethodPost, origin: []string{""}, host: "admin.example.test"},
		{name: "null Origin", method: http.MethodPost, origin: []string{"null"}, host: "admin.example.test"},
		{name: "duplicate Origin", method: http.MethodPut, origin: []string{"http://admin.example.test", "http://admin.example.test"}, host: "admin.example.test"},
		{name: "comma joined Origin", method: http.MethodPut, origin: []string{"http://admin.example.test, http://admin.example.test"}, host: "admin.example.test"},
		{name: "cross origin", method: http.MethodPost, origin: []string{"http://other.example.test"}, host: "admin.example.test"},
		{name: "HTTPS Origin on HTTP request", method: http.MethodPost, origin: []string{"https://admin.example.test"}, host: "admin.example.test"},
		{name: "HTTP Origin on TLS request", method: http.MethodPost, origin: []string{"http://admin.example.test"}, host: "admin.example.test", tls: true},
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

func TestValidateOriginTrustsForwardedProto(t *testing.T) {
	t.Run("same origin behind HTTPS proxy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://admin.example.test/admin", nil)
		req.Host = "admin.example.test"
		req.Header.Set("Origin", "https://admin.example.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		policy := OriginPolicy{TrustedProxies: []*net.IPNet{mustTestCIDR("192.0.2.0/24")}}
		if err := ValidateOriginWithPolicy(req, policy); err != nil {
			t.Fatalf("ValidateOrigin: expected trusted X-Forwarded-Proto to match, got %v", err)
		}
	})
	t.Run("cross-origin still rejected behind proxy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://admin.example.test/admin", nil)
		req.Host = "admin.example.test"
		req.Header.Set("Origin", "https://other.example.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		policy := OriginPolicy{TrustedProxies: []*net.IPNet{mustTestCIDR("192.0.2.0/24")}}
		if err := ValidateOriginWithPolicy(req, policy); !errors.Is(err, ErrInvalidOrigin) {
			t.Fatalf("ValidateOrigin rejected cross-origin: want ErrInvalidOrigin, got %v", err)
		}
	})
	t.Run("ignores Forwarded header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://admin.example.test/admin", nil)
		req.Host = "admin.example.test"
		req.Header.Set("Origin", "http://admin.example.test")
		req.Header.Set("Forwarded", "proto=https;host=proxy.example.test")
		if err := ValidateOrigin(req); err != nil {
			t.Fatalf("ValidateOrigin: should have used Origin.Host for matching, not Forwarded header: %v", err)
		}
	})
	t.Run("direct TLS still works", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://admin.example.test/admin", nil)
		req.Host = "admin.example.test"
		req.Header.Set("Origin", "https://admin.example.test")
		req.TLS = &tls.ConnectionState{}
		if err := ValidateOrigin(req); err != nil {
			t.Fatalf("ValidateOrigin: %v", err)
		}
	})
}

func TestValidateOriginUsesConfiguredExternalOrigin(t *testing.T) {
	external, err := url.Parse("https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://backend.local/admin", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Host = "backend.local"
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-Forwarded-Proto", "https")
	if err := ValidateOriginWithPolicy(req, OriginPolicy{ExternalOrigin: external, TrustedProxies: []*net.IPNet{mustTestCIDR("10.0.0.0/8")}}); err != nil {
		t.Fatalf("ValidateOriginWithPolicy: %v", err)
	}
}

func TestValidateOriginRejectsExternalOriginFromUntrustedProxy(t *testing.T) {
	external, _ := url.Parse("https://admin.example.test")
	req := httptest.NewRequest(http.MethodPost, "http://backend.local/admin", nil)
	req.RemoteAddr = "192.168.1.2:1234"
	req.Host = "backend.local"
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("X-Forwarded-Proto", "https")
	if err := ValidateOriginWithPolicy(req, OriginPolicy{ExternalOrigin: external, TrustedProxies: []*net.IPNet{mustTestCIDR("10.0.0.0/8")}}); !errors.Is(err, ErrInvalidOrigin) {
		t.Fatalf("error = %v, want ErrInvalidOrigin", err)
	}
}

func mustTestCIDR(value string) *net.IPNet {
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		panic(err)
	}
	return network
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

func TestClientIPPrefersUntrustedHopFromTrustedProxy(t *testing.T) {
	trusted := []*net.IPNet{mustTestCIDR("10.0.0.0/8")}
	tests := []struct {
		name      string
		remote    string
		forwarded string
		networks  []*net.IPNet
		want      string
	}{
		{name: "direct peer without proxy", remote: "203.0.113.7:1234", want: "203.0.113.7"},
		{name: "trusted proxy walks to untrusted hop", remote: "10.1.2.3:1234", forwarded: "198.51.100.9, 10.1.2.3", networks: trusted, want: "198.51.100.9"},
		{name: "all hops trusted falls back to leftmost", remote: "10.1.2.3:1234", forwarded: "10.1.2.3, 10.2.3.4", networks: trusted, want: "10.1.2.3"},
		{name: "untrusted peer ignores forwarded header", remote: "203.0.113.7:1234", forwarded: "198.51.100.9", networks: trusted, want: "203.0.113.7"},
		{name: "malformed header falls back to peer", remote: "10.1.2.3:1234", forwarded: "not-an-ip", networks: trusted, want: "10.1.2.3"},
		{name: "no configured networks ignores header", remote: "10.1.2.3:1234", forwarded: "198.51.100.9", want: "10.1.2.3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://admin.example.test/login", nil)
			request.RemoteAddr = test.remote
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-For", test.forwarded)
			}
			if got := ClientIP(request, test.networks); got != test.want {
				t.Fatalf("ClientIP = %q, want %q", got, test.want)
			}
		})
	}
}

// TestValidateOriginTrustsForwardedHostWithoutExternalOrigin locks in the
// audit fix for deployments without an ExternalOrigin: behind a trusted proxy
// the request.Host carries the proxy's own hostname (e.g. the in-cluster
// service name), so Origin checks against the public host must honour the
// X-Forwarded-Host header instead. Deployments not behind a trusted proxy
// keep matching against request.Host directly.
func TestValidateOriginTrustsForwardedHostWithoutExternalOrigin(t *testing.T) {
	t.Run("trusted proxy X-Forwarded-Host matches origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://backend.local/admin", nil)
		req.RemoteAddr = "10.1.2.3:1234"
		req.Host = "backend.local"
		req.Header.Set("Origin", "https://admin.example.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "admin.example.test")
		policy := OriginPolicy{TrustedProxies: []*net.IPNet{mustTestCIDR("10.0.0.0/8")}}
		if err := ValidateOriginWithPolicy(req, policy); err != nil {
			t.Fatalf("ValidateOriginWithPolicy forwarded host = %v", err)
		}
	})

	t.Run("trusted proxy without X-Forwarded-Host stays on request host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://backend.local/admin", nil)
		req.RemoteAddr = "10.1.2.3:1234"
		req.Host = "backend.local"
		req.Header.Set("Origin", "https://admin.example.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		policy := OriginPolicy{TrustedProxies: []*net.IPNet{mustTestCIDR("10.0.0.0/8")}}
		if err := ValidateOriginWithPolicy(req, policy); !errors.Is(err, ErrInvalidOrigin) {
			t.Fatalf("error = %v, want ErrInvalidOrigin without X-Forwarded-Host", err)
		}
	})

	t.Run("untrusted peer ignores X-Forwarded-Host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://admin.example.test/admin", nil)
		req.RemoteAddr = "192.168.1.2:1234"
		req.Host = "admin.example.test"
		req.Header.Set("Origin", "http://admin.example.test")
		req.Header.Set("X-Forwarded-Host", "evil.example.test")
		if err := ValidateOrigin(req); err != nil {
			t.Fatalf("ValidateOrigin: %v", err)
		}
	})
}
