package nvidia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"nvidia-router/internal/fault"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

const maxErrorBodyBytes = 8 << 10

var ErrProtocol = errors.New("NVIDIA models protocol error")

type Client struct {
	httpClient *http.Client
	descriptor Descriptor
	settings   runtimeconfig.Provider
	proxy      xkproxy.Provider
	directPool *directTransportPool
	closeOnce  sync.Once
}

type requestFactory func(context.Context) (*http.Request, error)

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

func NewClient(httpClient *http.Client, descriptor Descriptor, settings runtimeconfig.Provider, proxy xkproxy.Provider) (*Client, error) {
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

	return &Client{
		httpClient: httpClient,
		descriptor: descriptor,
		settings:   settings,
		proxy:      proxy,
		directPool: newDirectTransportPool(httpClient.Transport),
	}, nil
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
		return c.doDirect(ctx, snapshot, build)
	}
	if !c.proxy.Enabled() {
		return nil, xkproxy.NewTransportError(errors.New("proxy is disabled"))
	}
	response, _, retryable, err := c.doProxyAttempt(ctx, snapshot, build)
	if !retryable {
		return response, err
	}
	if ctx.Err() != nil {
		return nil, err
	}
	response, _, retryable, err = c.doProxyAttempt(ctx, snapshot, build)
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

func (c *Client) doProxyAttempt(ctx context.Context, snapshot runtimeconfig.Snapshot, build requestFactory) (*http.Response, bool, bool, error) {
	handle, err := c.proxy.Acquire(ctx, snapshot)
	if err != nil {
		return nil, false, false, err
	}
	var wrote atomic.Bool
	request, err := build(ctx)
	if err != nil {
		handle.Release()
		return nil, false, false, err
	}
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
			return nil, wrote.Load(), false, err
		}
		response.Body = newReleaseBody(response.Body, handle.Release)
		if err != nil {
			_ = response.Body.Close()
			return nil, wrote.Load(), false, err
		}
		return response, wrote.Load(), false, nil
	}
	if err == nil {
		err = errors.New("NVIDIA proxy transport returned no response")
	}
	if ctx.Err() != nil || wrote.Load() || firstByteTimer.Expired() {
		handle.Release()
		return nil, wrote.Load(), false, err
	}
	handle.Retire(xkproxy.RetireReasonTransportError)
	handle.Release()
	return nil, false, true, err
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
	release func()
	once    sync.Once
}

func newReleaseBody(body io.ReadCloser, release func()) *releaseBody {
	return &releaseBody{ReadCloser: body, release: release}
}

func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

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
