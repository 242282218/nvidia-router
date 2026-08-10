package nvidia

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nvidia-router/internal/runtimeconfig"
)

// stickyHarness pairs a CONNECT proxy that records the X-XK-Session it saw with an
// upstream TLS server that records the header it received, so a proxy-mode request can
// assert both halves of the sticky contract: the pool sees the header on the outer
// CONNECT, the NVIDIA target never does.
type stickyHarness struct {
	proxy         *connectProxy
	sessionHeader atomic.Value
	targetHeader  atomic.Value
	client        *Client
}

func newStickyHarness(t *testing.T) *stickyHarness {
	t.Helper()
	harness := &stickyHarness{proxy: newConnectProxy(t)}
	harness.proxy.recordConnect = func(request *http.Request) {
		harness.sessionHeader.Store(request.Header.Get(xkStickySessionHeader))
	}

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		harness.targetHeader.Store(request.Header.Get(xkStickySessionHeader))
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)

	manager := newProxyManager(t, harness.proxy.Address(), upstream.Client().Transport.(*http.Transport))
	descriptor := DefaultDescriptor()
	descriptor.Chat.URL = upstream.URL + "/v1/chat/completions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, manager)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	harness.client = client
	return harness
}

func (h *stickyHarness) chat(ctx context.Context) error {
	response, err := h.client.Chat(ctx, runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 1000}, "same-key", []byte(`{"model":"vendor/model"}`), false)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, err = io.ReadAll(response.Body)
	return err
}

// TestClientPinsSessionOnOuterConnectAndOmitsFromTarget locks in the sticky contract:
// the X-XK-Session header travels on the CONNECT the pool sees, never on the TLS
// request the NVIDIA target receives.
func TestClientPinsSessionOnOuterConnectAndOmitsFromTarget(t *testing.T) {
	harness := newStickyHarness(t)
	if err := harness.chat(WithStickySession(context.Background(), 42)); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := harness.sessionHeader.Load().(string); got != "42" {
		t.Fatalf("CONNECT X-XK-Session = %q, want 42", got)
	}
	if got := harness.targetHeader.Load().(string); got != "" {
		t.Fatalf("target request X-XK-Session = %q, want empty (must not leak to NVIDIA)", got)
	}
}

// TestClientOmitsStickySessionHeaderWithoutContextKey guards against a regression
// that leaks a session header into direct or unkeyed proxy requests.
func TestClientOmitsStickySessionHeaderWithoutContextKey(t *testing.T) {
	harness := newStickyHarness(t)
	if err := harness.chat(context.Background()); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := harness.sessionHeader.Load().(string); got != "" {
		t.Fatalf("CONNECT X-XK-Session = %q, want empty", got)
	}
	if got := harness.targetHeader.Load().(string); got != "" {
		t.Fatalf("target request X-XK-Session = %q, want empty", got)
	}
}

// recordSession makes the proxy stash the X-XK-Session header value from the CONNECT.
// It is defined as a method on connectProxy so sticky tests can wrap the handler.
