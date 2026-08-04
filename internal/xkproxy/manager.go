package xkproxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

type RetireReason string

const (
	RetireReasonTransportError RetireReason = "transport_error"
	RetireReasonShutdown       RetireReason = "shutdown"
)

type ErrorReason string

const (
	ReasonTransportFailed ErrorReason = "transport_failed"
	ReasonManagerClosed   ErrorReason = "manager_closed"
)

type Error struct {
	reason ErrorReason
	cause  error
}

func (e *Error) Error() string       { return "upstream proxy unavailable" }
func (e *Error) Unwrap() error       { return e.cause }
func (e *Error) Reason() ErrorReason { return e.reason }

func NewTransportError(cause error) *Error {
	return &Error{reason: ReasonTransportFailed, cause: cause}
}

type Provider interface {
	Configured() bool
	Enabled() bool
	Acquire(context.Context, runtimeconfig.Snapshot) (*Handle, error)
}

type Manager struct {
	mu         sync.Mutex
	proxyURL   *url.URL
	base       *http.Transport
	logger     *slog.Logger
	transports map[transportKey]*cachedTransport
	clock      uint64
	closed     bool
}

const maxCachedTransports = 8

type cachedTransport struct {
	transport *http.Transport
	lastUsed  uint64
}

type Handle struct {
	manager   *Manager
	key       transportKey
	transport *http.Transport
}

type transportKey struct {
	connectTimeoutMS   int
	firstByteTimeoutMS int
}

func New(proxyURL *url.URL, authKey string, base *http.Transport, logger *slog.Logger) (*Manager, error) {
	if err := validateProxyURL(proxyURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(authKey) == "" {
		return nil, errors.New("initialize proxy manager: proxy authentication key is required")
	}
	if base == nil {
		return nil, errors.New("initialize proxy manager: HTTP transport is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	withAuth := *proxyURL
	withAuth.User = url.UserPassword("proxy", authKey)
	return &Manager{
		proxyURL:   &withAuth,
		base:       base,
		logger:     logger,
		transports: make(map[transportKey]*cachedTransport),
	}, nil
}

func validateProxyURL(value *url.URL) error {
	if value == nil || !value.IsAbs() || value.Host == "" || value.User != nil || value.RawQuery != "" || value.ForceQuery || value.Fragment != "" || (value.Path != "" && value.Path != "/") {
		return errors.New("initialize proxy manager: proxy URL is invalid")
	}
	scheme := strings.ToLower(value.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("initialize proxy manager: proxy URL is invalid")
	}
	return nil
}

func (m *Manager) Configured() bool { return m != nil }

func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed
}

func (m *Manager) Acquire(ctx context.Context, snapshot runtimeconfig.Snapshot) (*Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("proxy manager is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, &Error{reason: ReasonManagerClosed}
	}
	key := transportKey{
		connectTimeoutMS:   snapshot.ConnectTimeoutMS,
		firstByteTimeoutMS: snapshot.FirstByteTimeoutMS,
	}
	entry := m.transports[key]
	if entry == nil {
		entry = &cachedTransport{transport: m.newTransport(key)}
		m.transports[key] = entry
	}
	m.clock++
	entry.lastUsed = m.clock
	if len(m.transports) > maxCachedTransports {
		m.evictLeastRecentlyUsed()
	}
	return &Handle{manager: m, key: key, transport: entry.transport}, nil
}

func (m *Manager) evictLeastRecentlyUsed() {
	var oldestKey transportKey
	var oldest uint64
	for key, entry := range m.transports {
		if oldest == 0 || entry.lastUsed < oldest {
			oldestKey = key
			oldest = entry.lastUsed
		}
	}
	entry := m.transports[oldestKey]
	entry.transport.CloseIdleConnections()
	delete(m.transports, oldestKey)
}

func (m *Manager) newTransport(key transportKey) *http.Transport {
	transport := m.base.Clone()
	transport.Proxy = http.ProxyURL(m.proxyURL)
	connectTimeout := time.Duration(key.connectTimeoutMS) * time.Millisecond
	baseDialContext := transport.DialContext
	if baseDialContext == nil {
		transport.DialContext = (&net.Dialer{Timeout: connectTimeout}).DialContext
	} else if connectTimeout > 0 {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			limited, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			return baseDialContext(limited, network, address)
		}
	}
	transport.ResponseHeaderTimeout = time.Duration(key.firstByteTimeoutMS) * time.Millisecond
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 60 * time.Second
	transport.ForceAttemptHTTP2 = true
	return transport
}

func (m *Manager) retire(handle *Handle, reason RetireReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if handle == nil || handle.manager != m {
		return
	}
	if current := m.transports[handle.key]; current != nil && current.transport == handle.transport {
		delete(m.transports, handle.key)
	}
	handle.transport.CloseIdleConnections()
	m.logger.Info("proxy_transport_retired", "reason", reason)
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, entry := range m.transports {
		entry.transport.CloseIdleConnections()
	}
	m.transports = nil
	m.logger.Info("proxy_manager_closed")
}

func (h *Handle) Transport() http.RoundTripper {
	if h == nil {
		return nil
	}
	return h.transport
}

func (h *Handle) Retire(reason RetireReason) {
	if h == nil || h.manager == nil {
		return
	}
	h.manager.retire(h, reason)
}

func (h *Handle) Release() {}
