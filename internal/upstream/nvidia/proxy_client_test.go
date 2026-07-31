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

	"nvidia-router/internal/clock"
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
	manager, fetches := newProxyManager(t, []string{"127.0.0.1:1", proxy.Address()}, upstream.Client().Transport.(*http.Transport))
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
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if upstreamRequests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1 successful replay", upstreamRequests.Load())
	}
	if proxy.Connects() != 1 {
		t.Fatalf("successful proxy CONNECTs = %d, want 1", proxy.Connects())
	}
	if fetches.Load() != 2 {
		t.Fatalf("proxy fetches = %d, want 2", fetches.Load())
	}
}

func TestClientDoesNotReplayAfterFirstByteDeadline(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	manager, fetches := newProxyManager(t, []string{"192.0.2.10:8000"}, base)
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
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1", fetches.Load())
	}

	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
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
	manager, fetches := newProxyManager(t, []string{proxy.Address()}, upstream.Client().Transport.(*http.Transport))
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
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1", fetches.Load())
	}
}

func newProxyManager(t *testing.T, addresses []string, base *http.Transport) (*xkproxy.Manager, *atomic.Int32) {
	t.Helper()
	var index atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		current := int(index.Add(1)) - 1
		if current >= len(addresses) {
			current = len(addresses) - 1
		}
		_, _ = io.WriteString(writer, addresses[current])
	}))
	t.Cleanup(api.Close)
	apiURL, err := url.Parse(api.URL + "?qty=1")
	if err != nil {
		t.Fatalf("parse proxy API URL: %v", err)
	}
	manager, err := xkproxy.New(apiURL, 3*time.Minute, 15*time.Second, base, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("xkproxy.New: %v", err)
	}
	t.Cleanup(manager.Close)
	return manager, &index
}

type fixedSettings struct{}

func (fixedSettings) Snapshot() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
}

type connectProxy struct {
	server   *http.Server
	listener net.Listener
	connects atomic.Int32
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

func (p *connectProxy) handle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodConnect {
		http.Error(writer, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	p.connects.Add(1)
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
