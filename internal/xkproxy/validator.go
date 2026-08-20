package xkproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	ErrProxyAddress = errors.New("invalid proxy address")
	ErrDial         = errors.New("dial upstream proxy failed")
	ErrTimeout      = errors.New("validation timed out")
	ErrProxyAuth    = errors.New("upstream proxy authentication failed")
	ErrStatus       = errors.New("unexpected validation status")
	// ErrSlowProxy reports a proxy that reached the validation target but took
	// longer than MaxLatency. It is still reachable, but admitting it would drag
	// first-token latency toward the slowest exit (audit H1): a pool whose
	// fastest member is slow has no fast baseline to demote against.
	ErrSlowProxy = errors.New("proxy slower than maximum acceptable latency")
)

const maxValidationBodyBytes = 1 << 20

type Validator struct {
	validationURL    string
	validationStatus int
	timeout          time.Duration
	maxLatency       time.Duration
	allowPrivateForTest bool

	mu         sync.Mutex
	transports map[string]*cachedValidationTransport
}

type cachedValidationTransport struct {
	transport   *http.Transport
	fingerprint string
	lastUsed    time.Time
}

func NewValidator(validationURL string, validationStatus int, timeout time.Duration) *Validator {
	return NewValidatorWithMaxLatency(validationURL, validationStatus, timeout, 0)
}

// NewValidatorWithMaxLatency additionally rejects proxies whose validation
// round-trip exceeds maxLatency. A zero maxLatency disables the slow-proxy gate.
func NewValidatorWithMaxLatency(validationURL string, validationStatus int, timeout time.Duration, maxLatency time.Duration) *Validator {
	return &Validator{
		validationURL:       validationURL,
		validationStatus:    validationStatus,
		timeout:             timeout,
		maxLatency:          maxLatency,
		allowPrivateForTest: false,
		transports:          make(map[string]*cachedValidationTransport),
	}
}

// NewValidatorForTest creates a validator that allows private IPs for testing.
// Production code must not use this constructor.
func NewValidatorForTest(validationURL string, validationStatus int, timeout time.Duration) *Validator {
	return &Validator{
		validationURL:       validationURL,
		validationStatus:    validationStatus,
		timeout:             timeout,
		maxLatency:          0,
		allowPrivateForTest: true,
		transports:          make(map[string]*cachedValidationTransport),
	}
}

// NewValidatorWithMaxLatencyForTest creates a validator with max latency that allows private IPs for testing.
func NewValidatorWithMaxLatencyForTest(validationURL string, validationStatus int, timeout time.Duration, maxLatency time.Duration) *Validator {
	return &Validator{
		validationURL:       validationURL,
		validationStatus:    validationStatus,
		timeout:             timeout,
		maxLatency:          maxLatency,
		allowPrivateForTest: true,
		transports:          make(map[string]*cachedValidationTransport),
	}
}

func (v *Validator) Validate(ctx context.Context, proxy Proxy) error {
	_, err := v.ValidateWithLatency(ctx, proxy)
	return err
}

func (v *Validator) ValidateWithLatency(ctx context.Context, proxy Proxy) (time.Duration, error) {
	proxyURL, err := proxy.URL()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrProxyAddress, err)
	}
	if proxyURL.Host == "" {
		return 0, fmt.Errorf("%w: empty host", ErrProxyAddress)
	}

	dialTimeout := v.timeout / 2
	if dialTimeout <= 0 {
		dialTimeout = v.timeout
	}
	transport := v.transportForWithURL(proxy, proxyURL, dialTimeout)

	attemptCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, v.validationURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create validation request: %w", err)
	}

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return time.Since(started), classifyValidationError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxValidationBodyBytes))
	latency := time.Since(started)

	if resp.StatusCode == http.StatusProxyAuthRequired && v.validationStatus != http.StatusProxyAuthRequired {
		return latency, fmt.Errorf("%w: status %d", ErrProxyAuth, resp.StatusCode)
	}
	if resp.StatusCode != v.validationStatus {
		return latency, fmt.Errorf("%w: got %d, want %d", ErrStatus, resp.StatusCode, v.validationStatus)
	}
	if v.maxLatency > 0 && latency > v.maxLatency {
		return latency, fmt.Errorf("%w: %v exceeds %v", ErrSlowProxy, latency, v.maxLatency)
	}
	return latency, nil
}

func classifyValidationError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("%w: %v", ErrDial, err)
	}
	return fmt.Errorf("request validation URL: %w", err)
}

func (v *Validator) transportForWithURL(proxy Proxy, proxyURL *url.URL, dialTimeout time.Duration) *http.Transport {
	// Reuse already-parsed URL to avoid double parsing (hot path in collector).
	// proxy.URL() is called once in ValidateWithLatency and must not be repeated
	// inside the lock (reference: caddy's parsed-URL caching in reverseproxy).
	fingerprint := fmt.Sprintf("%s|%s|%s", proxy.Key(), dialTimeout, v.timeout)
	key := proxy.Key()

	v.mu.Lock()
	defer v.mu.Unlock()

	if entry, ok := v.transports[key]; ok && entry.fingerprint == fingerprint {
		entry.lastUsed = time.Now()
		return entry.transport
	} else if ok {
		entry.transport.CloseIdleConnections()
		delete(v.transports, key)
	}

		transport := &http.Transport{
			DialContext:         safeDialContext(dialTimeout, v.allowPrivateForTest),
			TLSHandshakeTimeout: dialTimeout,
		// Keep-alives enabled with bounded idle pool so a proxy validated
		// every 5s reuses its CONNECT tunnel instead of paying TCP+TLS
		// handshake each cycle. MaxIdleConnsPerHost is raised from the
		// default 2 (which capped concurrent probes and misclassified
		// healthy proxies as dead, audit H7) to 10 so 4 proxies×3
		// concurrent validations do not queue.
		DisableKeepAlives:   false,
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
		// The validation target (NVIDIA) sits behind a CDN that speaks HTTP/2.
		// Like the request-path transport (manager.go), the probe's outer leg to
		// the proxy is plain HTTP CONNECT, so ForceAttemptHTTP2 only governs the
		// TLS connection to the TARGET inside the tunnel. Leaving it false made
		// every probe HTTP/1.1 while the target replied with h2 frames, so every
		// fetched proxy failed validation and the pool drained to stale
		// last-known-good exits (found in real 联调 2026-08-13).
		ForceAttemptHTTP2: true,
	}
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	v.transports[key] = &cachedValidationTransport{
		transport:   transport,
		fingerprint: fingerprint,
		lastUsed:    time.Now(),
	}
	v.evictStaleLocked(time.Now())
	return transport
}

func (v *Validator) evictStaleLocked(now time.Time) {
	for key, entry := range v.transports {
		if now.Sub(entry.lastUsed) > 5*time.Minute {
			entry.transport.CloseIdleConnections()
			delete(v.transports, key)
		}
	}
}

func (v *Validator) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()

	for key, entry := range v.transports {
		entry.transport.CloseIdleConnections()
		delete(v.transports, key)
	}
}
