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
	Acquire(context.Context, runtimeconfig.Snapshot, string) (*Handle, error)
}

type Manager struct {
	mu         sync.Mutex
	proxyURL   *url.URL
	pool       *Pool      // Built-in proxy pool (optional)
	collector  *Collector // Proxy collector (optional)
	base       *http.Transport
	logger     *slog.Logger
	transports map[transportKey]*cachedTransport
	clock      uint64
	closed     bool
}

const maxCachedTransports = 64

type cachedTransport struct {
	transport *http.Transport
	// proxyKey is the pool identity the transport was built against. It is fixed
	// when the entry is created and reused on cache hits so failure reporting
	// always names the proxy the transport actually dials, never whatever the
	// rotation cursor happens to point at on a later Acquire.
	proxyKey string
	lastUsed uint64
}

type Handle struct {
	manager   *Manager
	key       transportKey
	transport *http.Transport
	proxyKey  string // Track which proxy this handle uses for failure reporting
}

type transportKey struct {
	connectTimeoutMS   int
	firstByteTimeoutMS int
	// session isolates one NVIDIA key's exit so different keys never share a
	// CONNECT. The empty session is the shared pool for callers without affinity.
	session string
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

// NewWithPool creates a manager with built-in proxy pool. The authKey parameter
// is retained for signature symmetry with New (static proxy mode) but is unused
// by the pool mode: the collector authenticates to the upstream API through the
// CollectorConfig URL, not through a fixed proxy credential.
func NewWithPool(cfg CollectorConfig, authKey string, base *http.Transport, logger *slog.Logger) (*Manager, error) {
	if base == nil {
		return nil, errors.New("initialize proxy manager: HTTP transport is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	pool := NewPool()
	collector := NewCollector(cfg, pool, logger)

	return &Manager{
		pool:       pool,
		collector:  collector,
		base:       base,
		logger:     logger,
		transports: make(map[transportKey]*cachedTransport),
	}, nil
}

// StartCollector starts the proxy collection loop
func (m *Manager) StartCollector(ctx context.Context) {
	if m.collector != nil {
		m.collector.Start(ctx)
	}
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

func (m *Manager) Acquire(ctx context.Context, snapshot runtimeconfig.Snapshot, session string) (*Handle, error) {
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

	// Resolve the transport. A cache hit reuses the existing transport and its
	// bound proxy; only a cache miss needs a proxy from the pool. This keeps a
	// healthy cached connection usable even when the pool momentarily reports no
	// healthy proxy (e.g. all TTLs expired between fetches), and makes failure
	// reporting name the proxy the transport was actually built against rather
	// than whatever the rotation cursor returns on this Acquire.
	key := transportKey{
		connectTimeoutMS:   snapshot.ConnectTimeoutMS,
		firstByteTimeoutMS: snapshot.FirstByteTimeoutMS,
		session:            session,
	}
	entry := m.transports[key]
	if entry == nil {
		var selectedProxy Proxy
		var hasProxy bool
		if m.pool != nil {
			selectedProxy, hasProxy = m.pool.Get(time.Now())
			if !hasProxy {
				return nil, errors.New("no healthy proxy available")
			}
		}
		entry = &cachedTransport{transport: m.newTransport(key, selectedProxy), proxyKey: selectedProxy.Key()}
		m.transports[key] = entry
	} else if m.pool != nil && entry.proxyKey != "" && !m.pool.HasHealthy(entry.proxyKey, time.Now()) {
		// The cached transport is bound to a proxy that has since been ejected or
		// removed. Rebuild it against a fresh pool proxy instead of continuing to
		// dial a dead exit (audit H3). Only replace when the pool has an
		// alternative: when the pool is momentarily empty, the healthy cached
		// connection stays usable (the transport itself still works even if its
		// proxy row is gone from the pool).
		if selectedProxy, hasProxy := m.pool.Get(time.Now()); hasProxy {
			entry.transport.CloseIdleConnections()
			entry = &cachedTransport{transport: m.newTransport(key, selectedProxy), proxyKey: selectedProxy.Key()}
			m.transports[key] = entry
		}
	}
	m.clock++
	entry.lastUsed = m.clock
	if len(m.transports) > maxCachedTransports {
		m.evictLeastRecentlyUsed()
	}
	return &Handle{manager: m, key: key, transport: entry.transport, proxyKey: entry.proxyKey}, nil
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

func (m *Manager) newTransport(key transportKey, proxy Proxy) *http.Transport {
	transport := m.base.Clone()

	// If we have a proxy from the pool, use it; otherwise use the configured proxyURL
	if proxy.Address != "" {
		proxyURL, err := proxy.URL()
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else if m.proxyURL != nil {
		transport.Proxy = http.ProxyURL(m.proxyURL)
	}
	// Pin the session on the outer proxy request so the pool can bind the exit.
	// GetProxyConnectHeader is only consulted for HTTPS CONNECT, which is the only
	// path this router uses through the pool.
	if key.session != "" {
		transport.GetProxyConnectHeader = func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error) {
			header := make(http.Header)
			header.Set("X-XK-Session", key.session)
			return header, nil
		}
	}
	connectTimeout := time.Duration(key.connectTimeoutMS) * time.Millisecond
	baseDialContext := transport.DialContext
	// KeepAlive probes prevent NAT/firewall tables from silently dropping idle
	// CONNECT tunnels, which would appear as phantom transport errors on the next
	// request that tries to reuse the cached connection.
	keepAliveDialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	if baseDialContext == nil {
		transport.DialContext = keepAliveDialer.DialContext
	} else if connectTimeout > 0 {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			limited, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			return baseDialContext(limited, network, address)
		}
	}
	transport.ResponseHeaderTimeout = time.Duration(key.firstByteTimeoutMS) * time.Millisecond
	// Larger idle pools reduce connection churn when many sticky sessions are
	// active simultaneously; 90s keeps CONNECT tunnels alive past most
	// NAT/load-balancer idle timeouts without holding sockets indefinitely.
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 64
	transport.IdleConnTimeout = 90 * time.Second
	// CONNECT tunnels carry a plain HTTP/1.1 pipe to the target; forcing HTTP/2
	// on the outer leg rarely helps and can confuse proxy implementations that
	// do not negotiate h2 on the CONNECT path.
	transport.ForceAttemptHTTP2 = false
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

	// Report failure to pool if we have one
	if m.pool != nil && handle.proxyKey != "" {
		policy := EjectionPolicy{
			FailureLimit: 3,
			BaseDuration: 10 * time.Second,
			MaxDuration:  60 * time.Second,
			MaxEjections: 3,
		}
		m.pool.ReportFailure(handle.proxyKey, time.Now(), policy)
	}
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

	if m.collector != nil {
		_ = m.collector.Close()
	}

	for _, entry := range m.transports {
		entry.transport.CloseIdleConnections()
	}
	m.transports = nil
	m.logger.Info("proxy_manager_closed")
}

// PoolStatus returns current pool status for monitoring
func (m *Manager) PoolStatus() PoolStatus {
	if m == nil || m.pool == nil {
		return PoolStatus{}
	}
	now := time.Now()
	return PoolStatus{
		TotalSize:   m.pool.LiveSize(now),
		HealthySize: m.pool.Size(now),
		Proxies:     m.pool.List(now),
	}
}

type PoolStatus struct {
	TotalSize   int
	HealthySize int
	Proxies     []Proxy
}

// JSON-safe projection of a pooled proxy for the admin UI. Only non-sensitive,
// operator-visible quality fields are exposed: the exit address, its measured
// latency, remaining TTL, and isolation state.
type ProxyStatus struct {
	Address         string `json:"address"`
	LatencyEWMAMS   int64  `json:"latency_ewma_ms"`
	RemainingSeconds int   `json:"remaining_seconds"`
	Healthy         bool   `json:"healthy"`
	Ejected         bool   `json:"ejected"`
	SuccessCount    uint64 `json:"success_count"`
	FailureCount    uint64 `json:"failure_count"`
}

func (s PoolStatus) View() []ProxyStatus {
	now := time.Now()
	view := make([]ProxyStatus, 0, len(s.Proxies))
	for _, proxy := range s.Proxies {
		view = append(view, ProxyStatus{
			Address:          proxy.Address,
			LatencyEWMAMS:    proxy.LatencyEWMA.Milliseconds(),
			RemainingSeconds: int(proxy.RemainingLife(now) / time.Second),
			Healthy:          proxy.AvailableAt(now),
			Ejected:          proxy.EjectedAt(now),
			SuccessCount:     proxy.SuccessCount,
			FailureCount:     proxy.FailureCount,
		})
	}
	return view
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

// ReportLatency feeds the observed request latency of a successful response back
// into the pool's EWMA so selection preference reflects live quality, not just
// the collector's last probe (audit H4). A proxy that has quietly slowed down
// gets demoted by demoteSlow on subsequent Acquires. Best-effort: a nil pool or
// empty proxyKey (static proxy mode) is a no-op.
func (h *Handle) ReportLatency(latency time.Duration) {
	if h == nil || h.manager == nil || h.manager.pool == nil || h.proxyKey == "" || latency <= 0 {
		return
	}
	h.manager.pool.ReportSuccess(h.proxyKey, time.Now(), latency, EjectionPolicy{
		FailureLimit: 3,
		BaseDuration: 10 * time.Second,
		MaxDuration:  60 * time.Second,
		MaxEjections: 3,
		LatencyAlpha: 0.3,
	})
}

func (h *Handle) Release() {}
