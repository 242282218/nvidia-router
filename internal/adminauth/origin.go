package adminauth

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var ErrInvalidOrigin = errors.New("invalid request origin")

type OriginPolicy struct {
	ExternalOrigin *url.URL
	TrustedProxies []*net.IPNet
}

func ValidateOrigin(request *http.Request) error {
	return ValidateOriginWithPolicy(request, OriginPolicy{})
}

func ValidateOriginWithPolicy(request *http.Request, policy OriginPolicy) error {
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
	expectedScheme, expectedHost := requestSchemeAndHost(request, policy)
	if !strings.EqualFold(origin.Scheme, expectedScheme) || !strings.EqualFold(origin.Host, expectedHost) {
		return ErrInvalidOrigin
	}
	return nil
}

func OriginGuard(next http.Handler) http.Handler {
	return OriginGuardWithPolicy(next, OriginPolicy{})
}

func OriginGuardWithPolicy(next http.Handler, policy OriginPolicy) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := ValidateOriginWithPolicy(request, policy); err != nil {
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

func requestSchemeAndHost(request *http.Request, policy OriginPolicy) (string, string) {
	if policy.ExternalOrigin != nil {
		if trustedProxy(request, policy.TrustedProxies) {
			return policy.ExternalOrigin.Scheme, policy.ExternalOrigin.Host
		}
	}
	scheme := requestScheme(request, policy)
	host := request.Host
	// ExternalOrigin is the explicit, recommended way to lock the admin origin.
	// When it is unset but the immediate peer is a trusted proxy, request.Host
	// is the proxy's own host (e.g. the in-cluster service name) and Origin
	// checks would always fail. Fall back to X-Forwarded-Host from the trusted
	// hop so deployments without ExternalOrigin still get a meaningful check.
	// The header must be the very first comma-separated entry; a multi-hop
	// chain belongs to X-Forwarded-Proto + ExternalOrigin instead.
	if policy.ExternalOrigin == nil && trustedProxy(request, policy.TrustedProxies) {
		if forwarded := strings.TrimSpace(strings.SplitN(request.Header.Get("X-Forwarded-Host"), ",", 2)[0]); forwarded != "" {
			host = forwarded
		}
	}
	return scheme, host
}

func requestScheme(request *http.Request, policy OriginPolicy) string {
	if request.TLS != nil {
		return "https"
	}
	if !trustedProxy(request, policy.TrustedProxies) {
		return "http"
	}
	proto := strings.ToLower(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")))
	if proto == "https" || proto == "http" {
		return proto
	}
	return "http"
}

func trustedProxy(request *http.Request, networks []*net.IPNet) bool {
	if request == nil || len(networks) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the peer address to key per-client rate limits on. When the
// immediate peer is a trusted proxy, the right-most untrusted hop from
// X-Forwarded-For is preferred, so all users behind one proxy do not share a
// single bucket. Without a trusted proxy (or without a usable header) it falls
// back to the socket peer.
func ClientIP(request *http.Request, trusted []*net.IPNet) string {
	if request == nil {
		return ""
	}
	if trustedProxy(request, trusted) {
		if client := forwardedClientIP(request, trusted); client != "" {
			return client
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(request.RemoteAddr)
	}
	return host
}

// forwardedClientIP walks X-Forwarded-For from the nearest hop (right-most)
// toward the client, skipping addresses inside trusted networks, and returns
// the first untrusted address. If every listed hop is trusted, the left-most
// address is returned as a fallback.
func forwardedClientIP(request *http.Request, trusted []*net.IPNet) string {
	parts := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	for index := len(parts) - 1; index >= 0; index-- {
		ip := net.ParseIP(strings.TrimSpace(parts[index]))
		if ip == nil || ipInAnyNetwork(ip, trusted) {
			continue
		}
		return ip.String()
	}
	for _, part := range parts {
		if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func ipInAnyNetwork(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
