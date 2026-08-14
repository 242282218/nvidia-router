package xkproxy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Proxy represents an upstream proxy with its lifecycle metadata
type Proxy struct {
	Scheme      string
	Address     string
	Username    string
	Password    string
	FetchedAt   time.Time
	ValidatedAt time.Time
	ExpiresAt   time.Time
	HealthFails int

	// EjectedUntil temporarily removes a proxy from selection without deleting it
	EjectedUntil  time.Time
	EjectionCount int

	// LatencyEWMA smooths collector validation latency. It proves reachability,
	// not that NVIDIA accepts real traffic through this exit.
	LatencyEWMA   time.Duration
	SuccessCount  uint64
	FailureCount  uint64
	LastSuccessAt time.Time

	// Request quality is tracked separately from collector validation. A proxy
	// can answer the probe quickly while being throttled or unreliable for real
	// NVIDIA requests.
	RequestSuccessCount   uint64
	RequestFailureCount   uint64
	RequestFailureStreak  int
	RequestLatencyEWMA    time.Duration
	RequestLatencySamples int

	// HTTPFailCount is the consecutive application-level failures (429/5xx
	// through this exit) since the last real 2xx. Unlike transport failures it is
	// not itself proof the exit is dead, so isolation needs a longer pattern; a
	// genuine 2xx resets it (audit H8).
	HTTPFailCount int

	// LastRequestSuccessAt is when this exit last served a real 2xx through the
	// request path (ReportSuccess), NOT a collector validation. The pool uses it
	// to decide whether an HTTP failure is exit-specific or systemic: when no
	// exit has served a 2xx recently, repeated 429/5xx are a key-level or global
	// upstream condition and no exit gets blamed (audit H8).
	LastRequestSuccessAt time.Time

	// LastFailureAt and LastFailureStatus record the most recent transport or
	// HTTP failure observed through this exit for the operator view. The HTTP
	// count window in ReportHTTPFailure uses LastHTTPFailureAt to forget stale
	// patterns spread over hours.
	LastFailureAt     time.Time
	LastHTTPFailureAt time.Time

	// LastHTTPFailureStatus is the most recent 429/5xx status seen through this
	// exit; 0 when the exit has never produced one. 529 is deliberately never
	// treated as exit-specific (see ReportHTTPFailure).
	LastHTTPFailureStatus int

	// LatencySamples counts how many latency measurements fed the EWMA so the
	// UI can distinguish a fresh EWMA from one based on a single sample.
	LatencySamples int
}

func (p Proxy) Key() string {
	return strings.Join([]string{p.Scheme, p.Address, p.Username, p.Password}, "\x00")
}

func (p Proxy) URL() (*url.URL, error) {
	scheme := p.Scheme
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported proxy scheme %q", p.Scheme)
	}
	if p.Address == "" {
		return nil, fmt.Errorf("proxy address is empty")
	}
	parsed := &url.URL{Scheme: scheme, Host: p.Address}
	if p.Username != "" {
		parsed.User = url.UserPassword(p.Username, p.Password)
	}
	return parsed, nil
}

func ParseProxy(raw string) (Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Proxy{}, fmt.Errorf("proxy is empty")
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err == nil && parsed.User != nil && parsed.User.Username() == "" {
			return Proxy{}, fmt.Errorf("invalid proxy credentials")
		}
		if err != nil || parsed.Hostname() == "" || parsed.Port() == "" {
			return Proxy{}, fmt.Errorf("invalid proxy URL")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Proxy{}, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
		}
		if err := validateHostPort(parsed.Hostname(), parsed.Port()); err != nil {
			return Proxy{}, err
		}
		proxy := Proxy{Scheme: parsed.Scheme, Address: net.JoinHostPort(parsed.Hostname(), parsed.Port())}
		if parsed.User != nil {
			proxy.Username = parsed.User.Username()
			proxy.Password, _ = parsed.User.Password()
		}
		return proxy, nil
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if err := validateHostPort(host, port); err != nil {
			return Proxy{}, err
		}
		return Proxy{Scheme: "http", Address: net.JoinHostPort(host, port)}, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) == 4 {
		if err := validateHostPort(parts[0], parts[1]); err != nil {
			return Proxy{}, err
		}
		return Proxy{Scheme: "http", Address: net.JoinHostPort(parts[0], parts[1]), Username: parts[2], Password: parts[3]}, nil
	}
	return Proxy{}, fmt.Errorf("invalid proxy format")
}

func validateHostPort(host, portText string) error {
	if host == "" || strings.ContainsAny(host, " /\t\r\n") {
		return fmt.Errorf("invalid proxy host")
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return fmt.Errorf("invalid proxy host")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid proxy port")
	}
	return nil
}

// LiveAt reports whether the provider TTL still covers this proxy
func (p Proxy) LiveAt(now time.Time) bool {
	return p.Address != "" && now.Before(p.ExpiresAt)
}

// EjectedAt reports whether the proxy is inside its temporary isolation window
func (p Proxy) EjectedAt(now time.Time) bool {
	return now.Before(p.EjectedUntil)
}

// AvailableAt reports whether the proxy may serve new requests
func (p Proxy) AvailableAt(now time.Time) bool {
	return p.LiveAt(now) && !p.EjectedAt(now)
}

// RemainingLife reports how long the proxy stays inside its TTL, never negative
func (p Proxy) RemainingLife(now time.Time) time.Duration {
	if remaining := p.ExpiresAt.Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}
