package nvidia

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"nvidia-router/internal/fault"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

const maxErrorBodyBytes = 8 << 10

// proxyAcquireWaitBudget covers the collector's short empty-pool window. A
// request should wait for a freshly published exit instead of failing in a few
// milliseconds while the health gauge still reflects the previous pool, but a
// dead exit must still return promptly so the router can switch keys.
const proxyAcquireWaitBudget = 10 * time.Second

// xkStickySessionHeader is the outer-CONNECT session header the pool reads to bind
// every request of one NVIDIA key to the same exit IP. The pool strips it before it
// can reach the target, and the TLS layer keeps it out of the NVIDIA request headers.
const xkStickySessionHeader = "X-XK-Session"

// xkStickySessionSize bounds the session value the pool accepts as a header, matching
// the pool's own sticky header handling. It is intentionally independent of how many
// bytes the database key ID would need.
const xkStickySessionSize = 64

// xkStickySessionHMACKeySize is the per-process HMAC key used to derive the session
// label. A fresh random key per process keeps the label stable for the lifetime of
// the transport cache while preventing the proxy pool from reversing it back to the
// internal database key ID (or from correlating labels across process restarts).
const xkStickySessionHMACKeySize = 32

type stickySessionKeyCtx struct{}

type forwardedHeadersCtx struct{}

var forwardedHeaderAllowlist = map[string]struct{}{
	"OpenAI-Organization": {}, "OpenAI-Project": {},
	"Traceparent": {}, "Tracestate": {}, "Baggage": {},
}

func WithForwardedHeaders(ctx context.Context, headers http.Header) context.Context {
	copy := make(http.Header)
	for name := range forwardedHeaderAllowlist {
		if values := headers.Values(name); len(values) > 0 {
			copy[name] = append([]string(nil), values...)
		}
	}
	return context.WithValue(ctx, forwardedHeadersCtx{}, copy)
}

func applyForwardedHeaders(request *http.Request, ctx context.Context) {
	if headers, ok := ctx.Value(forwardedHeadersCtx{}).(http.Header); ok {
		for name, values := range headers {
			request.Header[name] = append([]string(nil), values...)
		}
	}
}

// WithStickySession carries the NVIDIA key id that should pin the pool's exit for
// this request. It is read in proxy mode only; direct mode ignores it. The label sent
// on the outer CONNECT is derived from this id (see stickySessionLabel), so callers
// never need to build their own session string.
func WithStickySession(ctx context.Context, keyID int64) context.Context {
	return context.WithValue(ctx, stickySessionKeyCtx{}, keyID)
}

func stickySessionFrom(ctx context.Context) (int64, bool) {
	value, ok := ctx.Value(stickySessionKeyCtx{}).(int64)
	return value, ok
}

// stickySessionLabel derives a stable, irreversible label from a key ID. The pool
// sees this label on the outer CONNECT and binds one exit per distinct value, so it
// must be deterministic for the same key within a process but must not reveal the
// database row id (a raw keyID in a header would let the proxy pool correlate its
// bindings with our internal numbering). HMAC-SHA256 with a per-process random key
// gives both properties without new dependencies.
func stickySessionLabel(secret []byte, keyID int64) string {
	mac := hmac.New(sha256.New, secret)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(keyID))
	_, _ = mac.Write(encoded[:])
	digest := mac.Sum(nil)
	if len(digest) > xkStickySessionSize/2 {
		digest = digest[:xkStickySessionSize/2]
	}
	return hex.EncodeToString(digest)
}

func proxyLogLabel(key string) string {
	parts := strings.SplitN(key, "\x00", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "unknown"
	}
	return parts[0] + "://" + parts[1]
}

// stickySessionLabelFor returns the memoized sticky label for a key ID. The
// label is deterministic for the key within a process (stickySessionKey is
// fixed), so the HMAC is computed once and reused on the hot path.
func (c *Client) stickySessionLabelFor(keyID int64) string {
	if cached, ok := c.stickyLabels.Load(keyID); ok {
		return cached.(string)
	}
	label := stickySessionLabel(c.stickySessionKey, keyID)
	cached, _ := c.stickyLabels.LoadOrStore(keyID, label)
	return cached.(string)
}

func newStickySessionKey() ([]byte, error) {
	key := make([]byte, xkStickySessionHMACKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate sticky session HMAC key: %w", err)
	}
	return key, nil
}

var ErrProtocol = errors.New("NVIDIA models protocol error")
var ErrEmptyResponse = errors.New("NVIDIA chat response contained no usable output")

type Client struct {
	httpClient *http.Client
	descriptor Descriptor
	settings   runtimeconfig.Provider
	proxy      xkproxy.Provider
	directPool *directTransportPool
	// providerID is the stable provider identifier reported by ID(). The default
	// is "nvidia"; the multi-provider wiring overrides it when constructing a
	// client for an OpenAI-compatible upstream like SiliconFlow.
	providerID string
	// stickySessionKey derives the per-process sticky label so the proxy pool never
	// sees the internal database key id. It is only used in proxy mode.
	stickySessionKey []byte
	// stickyLabels memoizes keyID -> sticky label: the label is stable for the
	// lifetime of the process (derived from stickySessionKey), so caching avoids
	// a per-request HMAC on the hot path.
	stickyLabels sync.Map
	closeOnce    sync.Once
}
type requestFactory func(context.Context) (*http.Request, error)

func requireNonstreamSemanticCompletion(response *http.Response) {
	if response == nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return
	}
	if marker, ok := response.Body.(interface{ RequireSemanticCompletion() }); ok {
		marker.RequireSemanticCompletion()
	}
}

type ValidationState uint8

const (
	ValidationValid ValidationState = iota
	ValidationInvalidCredential
	ValidationTemporarilyUnavailable
	ValidationIndeterminate
	ValidationProxyUnavailable
)

type ValidationResult struct {
	State     ValidationState
	Models    []string
	RequestID string
	SafeError string
	Fault     *fault.Fault
}

func NewClient(httpClient *http.Client, descriptor Descriptor, settings runtimeconfig.Provider, proxy xkproxy.Provider, providerIDs ...string) (*Client, error) {
	if httpClient == nil {
		return nil, errors.New("new NVIDIA client: HTTP client is required")
	}
	if settings == nil {
		return nil, errors.New("new NVIDIA client: runtime settings are required")
	}
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*http.Transport); !ok {
		if proxy != nil && proxy.Enabled() {
			return nil, errors.New("new NVIDIA client: proxy mode requires an HTTP transport")
		}
		return nil, errors.New("new NVIDIA client: direct mode requires an HTTP transport")
	}

	// The sticky label key is only needed when the proxy pool is enabled; direct mode
	// never sends a session header. Generate it unconditionally so the failure surface
	// is construction-time rather than per-request. A fresh random key per process keeps
	// the label stable within a process (so transports reuse it) but unreversible by the
	// proxy pool, and uncorrelated across restarts.
	stickyKey, err := newStickySessionKey()
	if err != nil {
		return nil, err
	}

	providerID := "nvidia"
	if len(providerIDs) > 0 && providerIDs[0] != "" {
		providerID = providerIDs[0]
	}
	return &Client{
		httpClient:       httpClient,
		descriptor:       descriptor,
		settings:         settings,
		proxy:            proxy,
		providerID:       providerID,
		directPool:       newDirectTransportPool(httpClient.Transport),
		stickySessionKey: stickyKey,
	}, nil
}

// ProviderID exposes the client's stable provider identifier (see ID).
func (c *Client) ProviderID() string {
	if c.providerID != "" {
		return c.providerID
	}
	return "nvidia"
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.directPool != nil {
			c.directPool.Close()
		}
		if c.httpClient != nil {
			closeIdleConnections(c.httpClient.Transport)
		}
	})
}

func (c *Client) Models(ctx context.Context, token string) ([]string, error) {
	response, err := c.modelsRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		discardErrorBody(response.Body)
		return nil, fmt.Errorf("get NVIDIA models: upstream returned HTTP %d", response.StatusCode)
	}
	models, err := parseModels(response.Body)
	if err != nil {
		return nil, fmt.Errorf("get NVIDIA models: %w", err)
	}
	return models, nil
}

func (c *Client) ValidateCredential(ctx context.Context, token string, now time.Time) ValidationResult {
	response, err := c.modelsRequest(ctx, token)
	if err != nil {
		if isProxyError(err) {
			return ValidationResult{State: ValidationProxyUnavailable, SafeError: "upstream proxy unavailable"}
		}
		classified := fault.Classify(nil, err, true, now)
		return ValidationResult{
			State: ValidationTemporarilyUnavailable, SafeError: "NVIDIA models request failed", Fault: &classified,
		}
	}
	defer func() { _ = response.Body.Close() }()
	result := ValidationResult{RequestID: allowedRequestID(response.Header)}
	if response.StatusCode == http.StatusOK {
		models, parseErr := parseModels(response.Body)
		if parseErr == nil {
			result.State = ValidationValid
			result.Models = models
			return result
		}
		classified := fault.Protocol(parseErr)
		result.Fault = &classified
		if errors.Is(parseErr, ErrProtocol) {
			result.State = ValidationIndeterminate
			result.SafeError = "NVIDIA models response was malformed"
		} else {
			result.State = ValidationTemporarilyUnavailable
			result.SafeError = "NVIDIA models response could not be read"
		}
		return result
	}

	classified := fault.Classify(response, nil, true, now)
	result.Fault = &classified
	result.SafeError = fmt.Sprintf("NVIDIA models returned HTTP %d", response.StatusCode)
	switch {
	case classified.DisableKey:
		result.State = ValidationInvalidCredential
	case classified.Retryable:
		result.State = ValidationTemporarilyUnavailable
	default:
		result.State = ValidationIndeterminate
	}
	return result
}

func (c *Client) modelsRequest(ctx context.Context, token string) (*http.Response, error) {
	response, err := c.do(ctx, c.settings.Snapshot(), func(ctx context.Context) (*http.Request, error) {
		request, err := c.descriptor.NewRequest(c.descriptor.Models, false, token)
		if err != nil {
			return nil, fmt.Errorf("create NVIDIA models request: %w", err)
		}
		return request.WithContext(ctx), nil
	})
	if err != nil {
		return nil, fmt.Errorf("send NVIDIA models request: %w", err)
	}
	return response, nil
}

func (c *Client) do(ctx context.Context, snapshot runtimeconfig.Snapshot, build requestFactory) (*http.Response, error) {
	snapshot = c.effectiveSnapshot(snapshot)
	if c.proxy == nil || !c.proxy.Configured() {
		observability.SetRouteMode(ctx, "direct")
		return c.doDirect(ctx, snapshot, build)
	}
	if !c.proxy.Enabled() {
		observability.SetRouteMode(ctx, "built-in")
		return nil, xkproxy.NewTransportError(errors.New("proxy is disabled"))
	}
	observability.SetRouteMode(ctx, "built-in")
	response, handle, _, retryable, err := c.doProxyAttempt(ctx, snapshot, build)
	if !retryable {
		return response, err
	}
	if ctx.Err() != nil {
		return nil, err
	}
	// Only a pure transport failure is worth one immediate replay. An error the
	// proxy produced from an HTTP response (e.g. a 5xx CONNECT answer) means the
	// proxy is up and already refused the request; replaying would just double
	// the upstream load on a known-bad path (audit R5). Such proxy-produced
	// answers are also NOT an NVIDIA key fault: wrap them as a proxy-rejected
	// error so the router's executeLease surfaces a 502 without a key switch.
	// The refusing exit is retired either way — without this a sticky session
	// would keep dialling the same dead exit on every later request (2026-08-25
	// clustered fast-502 analysis).
	if !replayableProxyError(err) {
		if handle != nil {
			handle.Retire(xkproxy.RetireReasonProxyRejected)
			handle.Release()
		}
		return nil, xkproxy.NewProxyRejectedError(err)
	}
	response, _, _, retryable, err = c.doProxyAttempt(ctx, snapshot, build)
	if err == nil || response != nil {
		return response, err
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if !retryable {
		return nil, err
	}
	return nil, xkproxy.NewTransportError(err)
}

// replayableProxyError reports whether a failed proxy transport attempt is worth
// one replay. Only connection-level failures qualify: a dial that never
// connected, a reset before any byte, or a timeout. Go wraps proxy HTTP error
// responses in a url.Error whose inner error is a plain errorString (the proxy's
// status text, e.g. "Service Unavailable"), not a net.Error — unwrap it so those
// 5xx answers fall out of the replay path instead of being retried.
func replayableProxyError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) ||
		// ETIMEDOUT: dial succeeded but connection timed out before any byte was
		// exchanged — the proxy may be overloaded rather than truly absent, so one
		// replay on a fresh transport is worth attempting (audit R5).
		errors.Is(err, syscall.ETIMEDOUT)
}

func (c *Client) effectiveSnapshot(snapshot runtimeconfig.Snapshot) runtimeconfig.Snapshot {
	if c.settings == nil {
		return snapshot
	}
	current := c.settings.Snapshot()
	if snapshot.ConnectTimeoutMS == 0 {
		snapshot.ConnectTimeoutMS = current.ConnectTimeoutMS
	}
	if snapshot.FirstByteTimeoutMS == 0 {
		snapshot.FirstByteTimeoutMS = current.FirstByteTimeoutMS
	}
	return snapshot
}

func (c *Client) doDirect(ctx context.Context, snapshot runtimeconfig.Snapshot, build requestFactory) (*http.Response, error) {
	request, err := build(ctx)
	if err != nil {
		return nil, err
	}
	transport := c.directPool.Get(snapshot)
	httpClient := *c.httpClient
	httpClient.Transport = transport
	return httpClient.Do(request)
}

func (c *Client) doProxyAttempt(ctx context.Context, snapshot runtimeconfig.Snapshot, build requestFactory) (*http.Response, *xkproxy.Handle, bool, bool, error) {
	// The session label travels through xkproxy.Acquire so the Manager can put the
	// CONNECT header on the outer proxy request. It must never be written to the
	// target request header: NVIDIA would see it and the pool would not. The label is
	// derived from the key ID with a per-process HMAC so the pool cannot read our
	// internal numbering back out of the header.
	session := ""
	if keyID, ok := stickySessionFrom(ctx); ok {
		session = c.stickySessionLabelFor(keyID)
	}
	acquireCtx := ctx
	cancelAcquire := func() {}
	if !snapshot.FirstByteDeadline.IsZero() {
		acquireCtx, cancelAcquire = context.WithDeadline(ctx, snapshot.FirstByteDeadline)
	}
	handle, err := xkproxy.AcquireWithWait(acquireCtx, c.proxy, snapshot, session, proxyAcquireWaitBudget)
	cancelAcquire()
	if err != nil {
		return nil, nil, false, false, err
	}
	var wrote atomic.Bool
	request, err := build(ctx)
	if err != nil {
		handle.Release()
		return nil, nil, false, false, err
	}
	started := time.Now()
	firstByteTimer := startFirstByteWatch(snapshot.FirstByteDeadline)
	trace := &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				wrote.Store(true)
			}
		},
		GotFirstResponseByte: func() {
			firstByteTimer.Stop()
		},
	}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	httpClient := *c.httpClient
	httpClient.Transport = handle.Transport()
	response, err := httpClient.Do(request)
	firstByteTimer.Stop()
	if response != nil {
		if response.Body == nil {
			handle.Release()
			if err == nil {
				err = errors.New("NVIDIA proxy transport returned response without body")
			}
			return nil, nil, wrote.Load(), false, err
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			// The response header marks the network-observable first-byte point;
			// full body generation time must never influence proxy IP ranking.
			handle.ReportRequestLatency(time.Since(started))
		}
		response.Body = newReleaseBody(response.Body, handle.Release, func() {
			if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
				handle.ReportLatency(0)
			}
		}, handle.ReportRequestFailure)
		if err != nil {
			_ = response.Body.Close()
			return nil, nil, wrote.Load(), false, err
		}
		// Feed the observed result back into the pool so selection reflects live
		// exit quality (audit H4/H8):
		//   - 2xx proves the exit serves traffic: report latency (which also
		//     clears any isolation window and resets the HTTP-failure pattern);
		//   - 429/5xx through this exit are load/throttle signals that may be
		//     exit-specific: report an HTTP failure so a rate-limited or blocked
		//     IP is isolated instead of staying "healthy and fastest" forever;
		//   - other 4xx are request-level faults that recur on any exit: neutral.
		// Latency is deliberately not fed on HTTP failures: a fast rejection must
		// not make a failing exit look fast.
		switch {
		case isProxyHTTPFault(response.StatusCode):
			handle.ReportHTTPFailure(response.StatusCode)
			if key := handle.ProxyKey(); key != "" {
				observability.RequestLogger(ctx).Debug("proxy_http_fault",
					"proxy", proxyLogLabel(key),
					"status", response.StatusCode,
				)
			}
		}
		return response, nil, wrote.Load(), false, nil
	}
	if err == nil {
		err = errors.New("NVIDIA proxy transport returned no response")
	}
	firstByteTimedOut := firstByteTimer.Expired() && wrote.Load() && !errors.Is(ctx.Err(), context.Canceled)
	if ctx.Err() != nil || wrote.Load() || firstByteTimer.Expired() {
		if firstByteTimedOut {
			handle.ReportRequestFailure()
			handle.Invalidate()
			err = fmt.Errorf("%w: %w", fault.ErrFirstByteTimeout, err)
		}
		handle.Release()
		return nil, nil, wrote.Load(), false, err
	}
	// A pure transport failure through a pooled exit is worth one replay, but it
	// is also the clearest signal that a specific exit is failing. Record the exit
	// identity at debug so an operator correlating a slow/failed request can see
	// which proxy it dialled without scraping the pool status page.
	if key := handle.ProxyKey(); key != "" {
		observability.RequestLogger(ctx).Debug("proxy_transport_error",
			"proxy", proxyLogLabel(key),
			"error", err,
		)
	}
	handle.Retire(xkproxy.RetireReasonTransportError)
	handle.Release()
	return nil, nil, false, true, err
}

// firstByteWatch reports whether the first-byte deadline elapsed before any
// response byte was observed. RoundTrip error paths must not rely on the
// request context (it stays alive for streaming body reads), so the deadline
// is enforced here instead. GotFirstResponseByte stops the timer; the
// ResponseHeaderTimeout on the proxy Transport is the primary guard for
// headers never arriving.
type firstByteWatch struct {
	timer   *time.Timer
	expired atomic.Bool
}

func startFirstByteWatch(deadline time.Time) *firstByteWatch {
	watch := &firstByteWatch{}
	if deadline.IsZero() {
		return watch
	}
	duration := time.Until(deadline)
	if duration < 0 {
		watch.expired.Store(true)
		return watch
	}
	// time.AfterFunc already runs its callback on its own goroutine, so
	// there is no need to spawn another one to babysit the timer: Stop
	// is invoked from GotFirstResponseByte and the post-Do call sites,
	// and a cancelled request context makes httpClient.Do return on its
	// own. Spawning a goroutine that selects on timer.C would actually
	// leak: AfterFunc-backed timers never deliver on their channel.
	watch.timer = time.AfterFunc(duration, func() { watch.expired.Store(true) })
	return watch
}

func (w *firstByteWatch) Stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}

func (w *firstByteWatch) Expired() bool { return w.expired.Load() }

type releaseBody struct {
	io.ReadCloser
	release          func()
	onComplete       func()
	onFailure        func()
	once             sync.Once
	terminal         atomic.Uint32
	semanticRequired atomic.Bool
	semanticEOF      atomic.Bool
}

func newReleaseBody(body io.ReadCloser, release func(), onComplete func(), onFailure func()) *releaseBody {
	return &releaseBody{ReadCloser: body, release: release, onComplete: onComplete, onFailure: onFailure}
}

func (b *releaseBody) Read(payload []byte) (int, error) {
	read, err := b.ReadCloser.Read(payload)
	if err == io.EOF {
		if b.semanticRequired.Load() {
			// Non-stream validators need to inspect the complete body before
			// deciding whether EOF is valid. Defer failure until Close so a
			// successful validator can call MarkComplete after ReadAll.
			b.semanticEOF.Store(true)
		} else {
			b.completeTerminal()
		}
	} else if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return read, err
		}
		b.failTerminal()
	}
	return read, err
}

// RequireSemanticCompletion makes ordinary EOF a failed stream termination.
func (b *releaseBody) RequireSemanticCompletion() {
	b.semanticRequired.Store(true)
}

// MarkComplete records semantic completion for framed streams whose terminal
// marker arrives before the underlying connection reaches EOF.
func (b *releaseBody) MarkComplete() {
	b.completeTerminal()
}

func (b *releaseBody) completeTerminal() {
	if !b.terminal.CompareAndSwap(0, 1) {
		return
	}
	if b.onComplete != nil {
		b.onComplete()
	}
}

func (b *releaseBody) failTerminal() {
	if !b.terminal.CompareAndSwap(0, 2) {
		return
	}
	if b.onFailure != nil {
		b.onFailure()
	}
}

func (b *releaseBody) Close() error {
	if b.semanticRequired.Load() && b.semanticEOF.Load() {
		b.failTerminal()
	}
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

// isProxyHTTPFault reports whether an upstream status may indicate a bad exit IP
// rather than a request-level or credential condition. 429/5xx mean the target
// throttled or failed while the request went through this exit; an exit that
// keeps producing them while others succeed is likely throttled or blocked
// itself (audit H8). 403 is deliberately excluded: it is as often a credential
// fault (a bad key fails with 403 on every exit) as an IP block, and blaming the
// pool for a key problem would eject every exit.
func isProxyHTTPFault(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, statusOverloaded:
		return true
	}
	return false
}

// statusOverloaded is NVIDIA's "overloaded" status, outside the standard 5xx
// range; the router already treats it as a retryable server fault.
const statusOverloaded = 529

func isProxyError(err error) bool {
	var proxyErr *xkproxy.Error
	return errors.As(err, &proxyErr)
}

func discardErrorBody(body io.Reader) {
	_, _ = io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
}

func allowedRequestID(headers http.Header) string {
	for _, header := range []string{"X-Request-Id", "X-Amzn-Requestid"} {
		if requestID := headers.Get(header); requestID != "" {
			return requestID
		}
	}
	return ""
}
