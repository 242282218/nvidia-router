package xkproxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/runtimeconfig"
)

type RetireReason string

const (
	RetireReasonRenewWindow    RetireReason = "renew_window"
	RetireReasonTransportError RetireReason = "transport_error"
	RetireReasonShutdown       RetireReason = "shutdown"
)

type fetchFunc func(context.Context) (*url.URL, error)

type Manager struct {
	mu                    sync.Mutex
	ttl                   time.Duration
	renewBefore           time.Duration
	base                  *http.Transport
	source                clock.Clock
	logger                *slog.Logger
	fetch                 fetchFunc
	active                *lease
	retiring              map[*lease]struct{}
	fetching              chan struct{}
	fetchCancel           context.CancelFunc
	lastFetchFailureUntil time.Time
	lastFetchError        *Error
	closeCh               chan struct{}
	closed                bool
	nextID                int64
	fetchCount            int64
	retiredCount          int64
}

type Handle struct {
	manager   *Manager
	lease     *lease
	transport http.RoundTripper
	released  atomicFlag
}

type lease struct {
	id             int64
	proxyURL       *url.URL
	acquiredAt     time.Time
	expiresAt      time.Time
	usableUntil    time.Time
	refs           int
	servedRequests int64
	reuseHits      int64
	state          leaseState
	retireReason   RetireReason
	transports     map[transportKey]*http.Transport
}

type leaseState uint8

const (
	leaseActive leaseState = iota
	leaseRetiring
)

type transportKey struct {
	connectTimeoutMS   int
	firstByteTimeoutMS int
}

type atomicFlag struct {
	mu   sync.Mutex
	done bool
}

func (f *atomicFlag) Do(callback func()) {
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		return
	}
	f.done = true
	f.mu.Unlock()
	callback()
}

func New(
	apiURL *url.URL,
	ttl time.Duration,
	renewBefore time.Duration,
	base *http.Transport,
	source clock.Clock,
	logger *slog.Logger,
) (*Manager, error) {
	if apiURL == nil || apiURL.Host == "" {
		return nil, errors.New("initialize proxy manager: API URL is required")
	}
	if ttl < 30*time.Second || ttl > 30*time.Minute {
		return nil, errors.New("initialize proxy manager: TTL is outside the supported range")
	}
	if renewBefore <= 0 || renewBefore >= ttl {
		return nil, errors.New("initialize proxy manager: renew window is invalid")
	}
	if base == nil {
		return nil, errors.New("initialize proxy manager: HTTP transport is required")
	}
	if source == nil {
		source = clock.RealClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return newManagerWithFetcher(apiURL, ttl, renewBefore, base, source, logger, newFetcher(apiURL).Fetch), nil
}

func newManagerWithFetcher(
	apiURL *url.URL,
	ttl time.Duration,
	renewBefore time.Duration,
	base *http.Transport,
	source clock.Clock,
	logger *slog.Logger,
	fetch fetchFunc,
) *Manager {
	_ = apiURL
	return &Manager{
		ttl:         ttl,
		renewBefore: renewBefore,
		base:        base,
		source:      source,
		logger:      logger,
		fetch:       fetch,
		retiring:    make(map[*lease]struct{}),
		closeCh:     make(chan struct{}),
	}
}

func (m *Manager) Acquire(ctx context.Context, snapshot runtimeconfig.Snapshot) (*Handle, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, newError(ReasonManagerClosed, nil)
		}
		now := m.source.Now()
		if m.active != nil && !now.Before(m.active.usableUntil) {
			m.retireLocked(m.active, RetireReasonRenewWindow)
		}
		if m.active != nil {
			handle := m.newHandleLocked(m.active, snapshot)
			m.mu.Unlock()
			return handle, nil
		}
		if m.lastFetchError != nil && now.Before(m.lastFetchFailureUntil) {
			err := *m.lastFetchError
			m.mu.Unlock()
			return nil, &err
		}
		if m.fetching != nil {
			wait := m.fetching
			closeCh := m.closeCh
			m.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-closeCh:
				return nil, newError(ReasonManagerClosed, nil)
			}
		}

		fetchDone := make(chan struct{})
		fetchCtx, cancel := context.WithCancel(ctx)
		m.fetching = fetchDone
		m.fetchCancel = cancel
		m.mu.Unlock()

		started := time.Now()
		proxyURL, fetchErr := m.fetch(fetchCtx)
		cancel()

		m.mu.Lock()
		m.fetching = nil
		m.fetchCancel = nil
		close(fetchDone)
		if m.closed {
			m.mu.Unlock()
			return nil, newError(ReasonManagerClosed, nil)
		}
		if fetchErr != nil || proxyURL == nil {
			if ctx.Err() != nil {
				m.mu.Unlock()
				return nil, ctx.Err()
			}
			proxyErr := asProxyError(fetchErr, ReasonInvalidResponse)
			m.lastFetchError = proxyErr
			m.lastFetchFailureUntil = m.source.Now().Add(time.Second)
			m.fetchCount++
			m.logger.Warn("proxy_fetch_failed", "error_reason", proxyErr.Reason())
			m.mu.Unlock()
			return nil, proxyErr
		}
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		lease := m.newLeaseLocked(proxyURL)
		m.active = lease
		m.fetchCount++
		m.logger.Info("proxy_lease_acquired", "lease_id", lease.id, "fetch_duration_ms", time.Since(started).Milliseconds(), "usable_duration_ms", m.ttl.Milliseconds()-m.renewBefore.Milliseconds())
		handle := m.newHandleLocked(lease, snapshot)
		m.mu.Unlock()
		return handle, nil
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	close(m.closeCh)
	if m.fetchCancel != nil {
		m.fetchCancel()
	}
	if m.active != nil {
		m.retireLocked(m.active, RetireReasonShutdown)
	}
	for lease := range m.retiring {
		m.closeLeaseTransportsLocked(lease)
		if lease.refs == 0 {
			m.removeLeaseLocked(lease)
		}
	}
	m.logger.Info("proxy_manager_closed", "fetch_count", m.fetchCount, "retired_count", m.retiredCount)
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
	h.manager.retireHandle(h, reason)
}

func (h *Handle) Release() {
	if h == nil || h.manager == nil {
		return
	}
	h.released.Do(func() { h.manager.release(h.lease) })
}

func (m *Manager) newLeaseLocked(proxyURL *url.URL) *lease {
	m.nextID++
	copyURL := *proxyURL
	now := m.source.Now()
	return &lease{
		id:          m.nextID,
		proxyURL:    &copyURL,
		acquiredAt:  now,
		expiresAt:   now.Add(m.ttl),
		usableUntil: now.Add(m.ttl - m.renewBefore),
		state:       leaseActive,
		transports:  make(map[transportKey]*http.Transport),
	}
}

func (m *Manager) newHandleLocked(lease *lease, snapshot runtimeconfig.Snapshot) *Handle {
	key := transportKey{connectTimeoutMS: snapshot.ConnectTimeoutMS, firstByteTimeoutMS: snapshot.FirstByteTimeoutMS}
	transport := lease.transports[key]
	if transport == nil {
		transport = m.newTransport(lease.proxyURL, key)
		lease.transports[key] = transport
	}
	if lease.servedRequests > 0 {
		lease.reuseHits++
	}
	lease.servedRequests++
	lease.refs++
	return &Handle{manager: m, lease: lease, transport: transport}
}

func (m *Manager) newTransport(proxyURL *url.URL, key transportKey) *http.Transport {
	transport := m.base.Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	connectTimeout := time.Duration(key.connectTimeoutMS) * time.Millisecond
	baseDialContext := transport.DialContext
	if baseDialContext == nil {
		transport.DialContext = (&net.Dialer{Timeout: connectTimeout}).DialContext
	} else {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if connectTimeout <= 0 {
				return baseDialContext(ctx, network, address)
			}
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

func (m *Manager) retireHandle(handle *Handle, reason RetireReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retireLocked(handle.lease, reason)
}

func (m *Manager) retireLocked(lease *lease, reason RetireReason) {
	if lease == nil || lease.state != leaseActive {
		return
	}
	lease.state = leaseRetiring
	lease.retireReason = reason
	if m.active == lease {
		m.active = nil
	}
	m.retiring[lease] = struct{}{}
	m.retiredCount++
	if reason == RetireReasonTransportError || reason == RetireReasonShutdown {
		m.closeLeaseTransportsLocked(lease)
	}
	m.logger.Info("proxy_lease_retired", "lease_id", lease.id, "served_requests", lease.servedRequests, "reuse_hits", lease.reuseHits, "active_requests", lease.refs, "retire_reason", reason)
	if lease.refs == 0 {
		m.removeLeaseLocked(lease)
	}
}

func (m *Manager) release(lease *lease) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lease == nil || lease.refs == 0 {
		return
	}
	lease.refs--
	if lease.refs == 0 && lease.state == leaseRetiring {
		m.removeLeaseLocked(lease)
	}
}

func (m *Manager) removeLeaseLocked(lease *lease) {
	delete(m.retiring, lease)
	m.closeLeaseTransportsLocked(lease)
	lease.transports = nil
}

func (m *Manager) closeLeaseTransportsLocked(lease *lease) {
	for _, transport := range lease.transports {
		transport.CloseIdleConnections()
	}
}

func asProxyError(err error, fallback ErrorReason) *Error {
	var proxyErr *Error
	if errors.As(err, &proxyErr) {
		return proxyErr
	}
	return newError(fallback, err)
}
