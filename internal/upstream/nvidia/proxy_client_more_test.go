package nvidia

import (
	"context"
	"errors"
	"io"
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

// TestClientBothProxiesFailReturnsProxyError proves that when both the first
// lease and the replay lease fail before the request is written, the client
// returns a stable xkproxy.Error instead of fetching a third address, and no
// byte ever reaches the NVIDIA upstream.
func TestClientBothProxiesFailReturnsProxyError(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	manager, fetches := newProxyManager(t, []string{"127.0.0.1:1"}, upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded with two broken proxies")
	}
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("error = %v, want *xkproxy.Error", err)
	}
	if proxyErr.Reason() != xkproxy.ReasonTransportFailed {
		t.Fatalf("proxy error reason = %q, want transport_failed", proxyErr.Reason())
	}
	if fetches.Load() != 2 {
		t.Fatalf("proxy fetches = %d, want 2 (no third fetch)", fetches.Load())
	}
	if upstreamRequests.Load() != 0 {
		t.Fatalf("upstream requests = %d, want 0", upstreamRequests.Load())
	}
}

// TestClientCancelDoesNotRetireHealthyProxy proves that a caller cancellation
// while the dial is in flight releases the lease without retiring the proxy,
// triggering no replay and no extra fetch.
func TestClientCancelDoesNotRetireHealthyProxy(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err = client.Chat(ctx, runtimeconfig.Snapshot{ConnectTimeoutMS: 10000, FirstByteTimeoutMS: 10000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded after cancel")
	}
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1", fetches.Load())
	}
	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("Acquire after cancel retired healthy proxy: %v", err)
	}
	handle.Release()
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches after acquire = %d, want 1 (healthy proxy not retired)", fetches.Load())
	}
}

// TestClientResponseHeaderTimeoutAfterWriteDoesNotRefreshProxy proves that a
// ResponseHeaderTimeout after the request was successfully written does not
// retire the proxy or fetch a new address; the error is left for the existing
// Attempt/Fault failover path.
func TestClientResponseHeaderTimeoutAfterWriteDoesNotRefreshProxy(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		// Block until the client gives up, with a hard bound so the test server
		// never hangs its own Close.
		select {
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
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

	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 300}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded despite response header timeout")
	}
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1 (no refresh after write)", fetches.Load())
	}
	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("Acquire after header timeout retired proxy: %v", err)
	}
	handle.Release()
}

// TestClientWroteRequestThenDisconnectDoesNotRetireProxy proves that a
// connection break after the response headers (request already written) is an
// upstream-path fault, not a proxy fault: no extra fetch, proxy stays usable.
func TestClientWroteRequestThenDisconnectDoesNotRetireProxy(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		conn, buffered, err := hijack(writer)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Declare a body larger than what is sent, then drop the connection so
		// the client observes a truncated body after the headers.
		_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 100\r\n\r\npartial")
		_ = buffered.Flush()
		_ = conn.Close()
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

	response, err := client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.HasPrefix(string(body), "partial") {
		t.Fatalf("response body = %q, want prefix partial", body)
	}
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1 (disconnect after write is not a proxy failure)", fetches.Load())
	}
	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("Acquire after post-write disconnect retired proxy: %v", err)
	}
	handle.Release()
}

// TestClientProxyModeForbidsDirectUpstream proves the request succeeds even
// when the base transport is hardened to reject every dial that is not the
// proxy address, i.e. there is no silent direct fallback.
func TestClientProxyModeForbidsDirectUpstream(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	proxy := newConnectProxy(t)
	// Clone the upstream's client transport so the proxy-mode transport keeps
	// the TLS roots that trust the httptest certificate, then harden the dial
	// to reject everything except the proxy address.
	base := upstream.Client().Transport.(*http.Transport).Clone()
	proxyAddress := proxy.Address()
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != proxyAddress {
			return nil, errors.New("direct dial blocked")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	manager, fetches := newProxyManager(t, []string{proxy.Address()}, base)
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
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if upstreamRequests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", upstreamRequests.Load())
	}
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1", fetches.Load())
	}
}

// TestNewClientRejectsCustomTransportInProxyMode proves the constructor fails
// safely when proxy mode is enabled but the base RoundTripper is not an
// *http.Transport.
func TestNewClientRejectsCustomTransportInProxyMode(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport)
	manager, _ := newProxyManager(t, []string{"127.0.0.1:1"}, base)
	custom := &customRoundTripper{base: base}
	_, err := NewClient(&http.Client{Transport: custom}, DefaultDescriptor(), fixedSettings{}, manager)
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("NewClient error = %v, want proxy mode transport error", err)
	}
}

type customRoundTripper struct {
	base http.RoundTripper
}

func (t *customRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(request)
}

// TestClientFetchFailureMakesZeroDirectRequests proves that when the fetch API
// itself fails, the request returns a proxy error and the NVIDIA upstream never
// sees a direct connection.
func TestClientFetchFailureMakesZeroDirectRequests(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)

	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "server error, not an ip:port")
	}))
	t.Cleanup(api.Close)
	apiURL, err := url.Parse(api.URL + "?qty=1")
	if err != nil {
		t.Fatalf("parse fetch URL: %v", err)
	}
	manager, err := xkproxy.New(apiURL, 3*time.Minute, 15*time.Second, upstream.Client().Transport.(*http.Transport), clock.RealClock{}, nil)
	if err != nil {
		t.Fatalf("xkproxy.New: %v", err)
	}
	t.Cleanup(manager.Close)
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err == nil {
		t.Fatal("Chat succeeded despite failing fetch")
	}
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) || proxyErr.Reason() != xkproxy.ReasonInvalidResponse {
		t.Fatalf("error = %v, want xkproxy.Error invalid_response", err)
	}
	if upstreamRequests.Load() != 0 {
		t.Fatalf("upstream requests = %d, want 0 (no direct fallback)", upstreamRequests.Load())
	}
}

// TestModelsAndAudioEndpointsUseProxy proves Models, AudioTranscriptions and
// AudioSpeech all traverse the shared proxy lease, not just Chat.
func TestModelsAndAudioEndpointsUseProxy(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamRequests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(writer, `{"data":[{"id":"vendor/model"}]}`)
		case "/v1/audio/transcriptions":
			_, _ = io.WriteString(writer, `{"text":"hello transcript"}`)
		case "/v1/audio/speech":
			writer.Header().Set("Content-Type", "audio/mpeg")
			_, _ = writer.Write([]byte("MP3BYTES"))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)
	proxy := newConnectProxy(t)
	manager, fetches := newProxyManager(t, []string{proxy.Address()}, upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Models.URL = upstream.URL + "/v1/models"
	descriptor.ASR.URL = upstream.URL + "/v1/audio/transcriptions"
	descriptor.TTS.URL = upstream.URL + "/v1/audio/speech"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	models, err := client.Models(context.Background(), "same-key")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0] != "vendor/model" {
		t.Fatalf("models = %v", models)
	}

	asrResponse, err := client.AudioTranscriptions(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte("multipart-body"), "multipart/form-data; boundary=xyz")
	if err != nil {
		t.Fatalf("AudioTranscriptions: %v", err)
	}
	asrBody, _ := io.ReadAll(asrResponse.Body)
	_ = asrResponse.Body.Close()
	if !strings.Contains(string(asrBody), "hello transcript") {
		t.Fatalf("ASR body = %s", asrBody)
	}

	ttsResponse, err := client.AudioSpeech(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"input":"hello"}`))
	if err != nil {
		t.Fatalf("AudioSpeech: %v", err)
	}
	ttsBody, _ := io.ReadAll(ttsResponse.Body)
	_ = ttsResponse.Body.Close()
	if string(ttsBody) != "MP3BYTES" {
		t.Fatalf("TTS body = %q", ttsBody)
	}

	if upstreamRequests.Load() != 3 {
		t.Fatalf("upstream requests = %d, want 3", upstreamRequests.Load())
	}
	if proxy.Connects() < 1 {
		t.Fatalf("proxy CONNECTs = %d, want >= 1", proxy.Connects())
	}
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1 (single shared lease)", fetches.Load())
	}
}

// TestClientBodyCloseKeepsPoolWarm proves that closing the response body does
// not tear down the proxy connection pool: sequential requests reuse the same
// CONNECT tunnel.
func TestClientBodyCloseKeepsPoolWarm(t *testing.T) {
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

	for range 3 {
		response, err := client.Chat(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
		if err != nil {
			t.Fatalf("Chat: %v", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if err := response.Body.Close(); err != nil {
			t.Fatalf("close response: %v", err)
		}
	}
	if upstreamRequests.Load() != 3 {
		t.Fatalf("upstream requests = %d, want 3", upstreamRequests.Load())
	}
	if proxy.Connects() != 1 {
		t.Fatalf("proxy CONNECTs = %d, want 1 (pool stayed warm across body closes)", proxy.Connects())
	}
	if fetches.Load() != 1 {
		t.Fatalf("proxy fetches = %d, want 1", fetches.Load())
	}
}
