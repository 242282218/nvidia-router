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
	got := harness.sessionHeader.Load().(string)
	if got == "" {
		t.Fatal("CONNECT X-XK-Session is empty, want a sticky label")
	}
	if len(got) > xkStickySessionSize {
		t.Fatalf("CONNECT X-XK-Session length = %d, want <= %d", len(got), xkStickySessionSize)
	}
	if got == "42" {
		t.Fatal("CONNECT X-XK-Session is the raw key id; it must be an irreversible label")
	}
	if want := stickySessionLabel(harness.client.stickySessionKey, 42); got != want {
		t.Fatalf("CONNECT X-XK-Session = %q, want derived label %q", got, want)
	}
	if target := harness.targetHeader.Load().(string); target != "" {
		t.Fatalf("target request X-XK-Session = %q, want empty (must not leak to NVIDIA)", target)
	}
}

// TestClientStickySessionLabelIsStableAndKeyed locks in the label properties: the same
// key and secret always derive the same label, different keys never collide with a
// digest check, and the label leaks no key-id bits.
func TestClientStickySessionLabelIsStableAndKeyed(t *testing.T) {
	key, err := newStickySessionKey()
	if err != nil {
		t.Fatalf("newStickySessionKey: %v", err)
	}
	first := stickySessionLabel(key, 7)
	second := stickySessionLabel(key, 7)
	if first != second {
		t.Fatal("same key produced different sticky labels")
	}
	other := stickySessionLabel(key, 8)
	if first == other {
		t.Fatal("distinct key ids produced the same sticky label")
	}
	if first == "7" {
		t.Fatal("sticky label leaked the raw key id")
	}
	otherKey, err := newStickySessionKey()
	if err != nil {
		t.Fatalf("newStickySessionKey: %v", err)
	}
	if stickySessionLabel(otherKey, 7) == first {
		t.Fatal("sticky label is not keyed by the per-process secret")
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

// TestClientSameKeyReusesConnectAndDifferentKeysDoNot proves that the session label is
// part of the transport identity: requests for the same NVIDIA key share the CONNECT
// tunnel, requests for different keys never do.
func TestClientSameKeyReusesConnectAndDifferentKeysDoNot(t *testing.T) {
	harness := newStickyHarness(t)
	sameKey := WithStickySession(context.Background(), 11)
	if err := harness.chat(sameKey); err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	firstConnects := harness.proxy.Connects()
	if err := harness.chat(sameKey); err != nil {
		t.Fatalf("second Chat: %v", err)
	}
	if got := harness.proxy.Connects(); got != firstConnects {
		t.Fatalf("same-key CONNECTs grew from %d to %d, want reuse", firstConnects, got)
	}

	otherKey := WithStickySession(context.Background(), 12)
	if err := harness.chat(otherKey); err != nil {
		t.Fatalf("different-key Chat: %v", err)
	}
	if got := harness.proxy.Connects(); got <= firstConnects {
		t.Fatalf("different-key CONNECTs = %d, want a fresh tunnel", got)
	}
}
