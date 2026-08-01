package adminauth

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

var ErrInvalidOrigin = errors.New("invalid request origin")

func ValidateOrigin(request *http.Request) error {
	if request == nil || !requiresOrigin(request.Method) {
		return nil
	}
	origins := request.Header.Values("Origin")
	if len(origins) != 1 {
		return ErrInvalidOrigin
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" {
		return ErrInvalidOrigin
	}
	if !strings.EqualFold(origin.Scheme, requestScheme(request)) || !strings.EqualFold(origin.Host, request.Host) {
		return ErrInvalidOrigin
	}
	return nil
}

func OriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := ValidateOrigin(request); err != nil {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func requiresOrigin(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// requestScheme returns the effective URI scheme of a request.
//
// When deployed behind a TLS-terminating reverse proxy, request.TLS is nil
// because the proxy speaks plain HTTP to the backend.  The proxy forwards the
// original client scheme via X-Forwarded-Proto, so we read that header first
// and fall back to request.TLS for direct TLS connections.
//
// An attacker who can forge X-Forwarded-Proto can also forge the Host header
// (which is the other half of the origin check), so this does not weaken CSRF
// protection.
func requestScheme(request *http.Request) string {
	if proto := request.Header.Get("X-Forwarded-Proto"); proto == "https" || proto == "http" {
		return proto
	}
	if request.TLS != nil {
		return "https"
	}
	return "http"
}
