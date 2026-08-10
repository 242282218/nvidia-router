package nvidia

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

func TestNewClientRequiresRuntimeSettings(t *testing.T) {
	_, err := NewClient(http.DefaultClient, DefaultDescriptor(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "runtime settings") {
		t.Fatalf("NewClient error = %v, want runtime settings error", err)
	}
}

func TestClientUsesRuntimeSettingsForEmptySnapshot(t *testing.T) {
	client := &Client{settings: fixedSettings{}}
	got := client.effectiveSnapshot(runtimeconfig.Snapshot{})
	if got.ConnectTimeoutMS != 1000 || got.FirstByteTimeoutMS != 2000 {
		t.Fatalf("effective snapshot = %+v, want runtime timeout values", got)
	}

	explicit := client.effectiveSnapshot(runtimeconfig.Snapshot{ConnectTimeoutMS: 300, FirstByteTimeoutMS: 400})
	if explicit.ConnectTimeoutMS != 300 || explicit.FirstByteTimeoutMS != 400 {
		t.Fatalf("explicit snapshot was overwritten: %+v", explicit)
	}
}

func TestClientRetriesProxyFailureBeforeRequestWrite(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamRequests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer same-key" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	proxy := newConnectProxy(t)
	// A pure transport failure (connection dropped before any HTTP response) is
	// worth one replay; the second CONNECT succeeds and the request goes through.
	proxy.FailNextConnectRaw()
	manager := newProxyManager(t, proxy.Address(), upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	response, err := client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if upstreamRequests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1 successful replay", upstreamRequests.Load())
	}
	if proxy.Connects() != 2 {
		t.Fatalf("proxy CONNECTs = %d, want 2 (one failed and one successful attempt)", proxy.Connects())
	}
}

// TestClientDoesNotReplayProxy5xxConnectResponse locks in the R2.2 tightening: a
// 5xx CONNECT answer from the proxy means the proxy is up and already refused
// the request, so the client must not replay — replaying would double upstream
// load on a path known to be failing. It also asserts the error surfaces as an
// xkproxy.Error so the router short-circuits instead of cooldowning the key.
func TestClientDoesNotReplayProxy5xxConnectResponse(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	proxy := newConnectProxy(t)
	proxy.FailNextConnect() // proxy answers CONNECT with 502
	manager := newProxyManager(t, proxy.Address(), upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded despite a 5xx proxy CONNECT response")
	}
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("error = %T %v, want *xkproxy.Error so the router does not cooldown the key", err, err)
	}
	if proxyErr.Reason() != xkproxy.ReasonTransportFailed {
		t.Fatalf("proxy error reason = %q, want transport_failed", proxyErr.Reason())
	}
	if proxy.Connects() != 1 {
		t.Fatalf("proxy CONNECTs = %d, want 1 (5xx must not be replayed)", proxy.Connects())
	}
	if upstreamRequests.Load() != 0 {
		t.Fatalf("upstream requests = %d, want 0", upstreamRequests.Load())
	}
}

// TestClientWrapsProxyAuthRequiredConnectAsProxyError proves a 407 CONNECT answer
// (proxy authentication rejected) is also classified as a proxy fault, not an
// NVIDIA key fault, and is never replayed.
func TestClientWrapsProxyAuthRequiredConnectAsProxyError(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	proxy := newConnectProxy(t)
	proxy.FailNextConnectStatus(http.StatusProxyAuthRequired)
	manager := newProxyManager(t, proxy.Address(), upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded despite a 407 proxy CONNECT response")
	}
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("error = %T %v, want *xkproxy.Error", err, err)
	}
	if proxy.Connects() != 1 {
		t.Fatalf("proxy CONNECTs = %d, want 1 (407 must not be replayed)", proxy.Connects())
	}
}

func TestClientDoesNotReplayAfterFirstByteDeadline(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	manager := newProxyManager(t, "192.0.2.10:8000", base)
	client, err := NewClient(http.DefaultClient, DefaultDescriptor(), fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	deadline := time.Now().Add(20 * time.Millisecond)
	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{
		ConnectTimeoutMS:   1000,
		FirstByteTimeoutMS: 1000,
		FirstByteDeadline:  deadline,
	}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded after first-byte deadline")
	}
	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{}, "")
	if err != nil {
		t.Fatalf("Acquire after deadline: %v", err)
	}
	handle.Release()
}

func TestClientReusesProxyTransportAcrossSequentialResponses(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	proxy := newConnectProxy(t)
	manager := newProxyManager(t, proxy.Address(), upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	for range 20 {
		response, err := client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
		if err != nil {
			t.Fatalf("Chat: %v", err)
		}
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatalf("read response: %v", err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatalf("close response: %v", err)
		}
	}
	if upstreamRequests.Load() != 20 {
		t.Fatalf("upstream requests = %d, want 20", upstreamRequests.Load())
	}
	if proxy.Connects() > 2 {
		t.Fatalf("proxy CONNECTs = %d, want <= 2", proxy.Connects())
	}
}

func newProxyManager(t *testing.T, address string, base *http.Transport) *xkproxy.Manager {
	t.Helper()
	proxyURL, err := url.Parse("http://" + address)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	manager, err := xkproxy.New(proxyURL, "proxy-secret", base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("xkproxy.New: %v", err)
	}
	t.Cleanup(manager.Close)
	return manager
}

type fixedSettings struct{}

func (fixedSettings) Snapshot() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
}

type connectProxy struct {
	server      *http.Server
	listener    net.Listener
	connects    atomic.Int32
	failNext    atomic.Int32
	failNextRaw atomic.Bool
	// recordConnect is an optional hook invoked for every CONNECT with the request.
	// It lets sticky tests observe the outer header the pool would read.
	recordConnect func(*http.Request)
}

func newConnectProxy(t *testing.T) *connectProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	proxy := &connectProxy{listener: listener}
	proxy.server = &http.Server{Handler: http.HandlerFunc(proxy.handle)}
	go func() { _ = proxy.server.Serve(listener) }()
	t.Cleanup(func() {
		_ = proxy.server.Close()
		_ = listener.Close()
	})
	return proxy
}

func (p *connectProxy) Address() string { return p.listener.Addr().String() }

func (p *connectProxy) Connects() int32 { return p.connects.Load() }

// FailNextConnect makes the next CONNECT answer with an HTTP 5xx status, which
// the client surfaces as a transport error that must NOT be replayed.
func (p *connectProxy) FailNextConnect() { p.failNext.Store(http.StatusBadGateway) }

// FailNextConnectStatus makes the next CONNECT answer with an explicit status code,
// so a test can distinguish a 407 auth rejection from a generic 5xx.
func (p *connectProxy) FailNextConnectStatus(status int) { p.failNext.Store(int32(status)) }

// FailNextConnectRaw makes the next CONNECT fail at the transport level: the
// proxy accepts the tunnel then closes it without an HTTP response, so the
// client observes a genuine connection error rather than a proxy HTTP status.
func (p *connectProxy) FailNextConnectRaw() { p.failNextRaw.Store(true) }

func (p *connectProxy) handle(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Proxy-Authorization") != "Basic cHJveHk6cHJveHktc2VjcmV0" {
		http.Error(writer, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if request.Method != http.MethodConnect {
		http.Error(writer, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	if p.recordConnect != nil {
		p.recordConnect(request)
	}
	p.connects.Add(1)
	if p.failNextRaw.CompareAndSwap(true, false) {
		client, buffered, err := hijack(writer)
		if err != nil {
			return
		}
		_ = buffered.Flush()
		_ = client.Close()
		return
	}
	if status := p.failNext.Load(); status != 0 {
		p.failNext.Store(0)
		http.Error(writer, "temporary proxy failure", int(status))
		return
	}
	upstream, err := net.DialTimeout("tcp", request.Host, time.Second)
	if err != nil {
		http.Error(writer, "upstream unavailable", http.StatusBadGateway)
		return
	}
	client, buffered, err := hijack(writer)
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		_ = client.Close()
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	_ = upstream.Close()
}

func hijack(writer http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("proxy writer does not support hijacking")
	}
	return hijacker.Hijack()
}
