package opencodefree

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

// flakyProxyProvider fronts a real static exit and can make the first N leases
// fail, standing in for dead entries in the pool.
type flakyProxyProvider struct {
	inner    *xkproxy.Manager
	failures int32
	acquires atomic.Int32
}

func (p *flakyProxyProvider) Configured() bool { return true }

func (p *flakyProxyProvider) Enabled() bool { return true }

func (p *flakyProxyProvider) Acquire(ctx context.Context, snapshot runtimeconfig.Snapshot, session string) (*xkproxy.Handle, error) {
	if p.acquires.Add(1) <= p.failures {
		return nil, xkproxy.NewTransportError(errors.New("exit is gone"))
	}
	return p.inner.Acquire(ctx, snapshot, session)
}

// proxiedClient wires a client whose gateway calls all leave through handler,
// standing in for a pooled exit. failures leases fail before any exit is dialed.
func proxiedClient(t *testing.T, failures int32, handler http.HandlerFunc) (*Client, *flakyProxyProvider) {
	t.Helper()
	exit := httptest.NewServer(handler)
	t.Cleanup(exit.Close)
	exitURL, err := url.Parse(exit.URL)
	if err != nil {
		t.Fatalf("parse exit URL: %v", err)
	}
	manager, err := xkproxy.New(exitURL, "proxy-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("xkproxy.New: %v", err)
	}
	t.Cleanup(manager.Close)

	// The gateway host is never dialed directly: every request is addressed to it
	// but travels through the exit above.
	baseURL, err := url.Parse("http://opencodefree.invalid/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	client, err := NewClient(&http.Client{}, baseURL, "local-entry-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	provider := &flakyProxyProvider{inner: manager, failures: failures}
	return client.WithProxy(provider), provider
}

// A dead exit must cost a retry rather than the whole request: the next lease
// carries the call, which is what keeps discovery alive through pool churn.
func TestModelsRetriesThroughAnotherExitAfterADeadOne(t *testing.T) {
	var served atomic.Int32
	client, provider := proxiedClient(t, 1, func(writer http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		_, _ = io.WriteString(writer, `{"data":[{"id":"model-free"}]}`)
	})

	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if strings.Join(models, ",") != "model-free" {
		t.Fatalf("models = %v", models)
	}
	if got := provider.acquires.Load(); got != 2 {
		t.Fatalf("leases = %d, want 2 (one dead exit then a live one)", got)
	}
	if got := served.Load(); got != 1 {
		t.Fatalf("gateway calls = %d, want 1", got)
	}
}

func TestModelsStopsAfterProxyAttemptsAreExhausted(t *testing.T) {
	client, provider := proxiedClient(t, 99, func(http.ResponseWriter, *http.Request) {
		t.Error("request reached the exit although every lease failed")
	})

	if _, err := client.Models(context.Background()); err == nil {
		t.Fatal("Models succeeded without a usable exit")
	}
	if got := provider.acquires.Load(); got != proxyAttempts {
		t.Fatalf("leases = %d, want %d", got, proxyAttempts)
	}
}

// A gateway answer is authoritative even when it is an error: replaying it could
// double a completion the gateway already accepted.
func TestChatDoesNotRetryAnAnsweredRequest(t *testing.T) {
	var served atomic.Int32
	client, _ := proxiedClient(t, 0, func(writer http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		writer.WriteHeader(http.StatusTooManyRequests)
	})

	response, err := client.Chat(context.Background(), runtimeconfig.Snapshot{}, []byte(`{"model":"model-free"}`), false)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.StatusCode)
	}
	if got := served.Load(); got != 1 {
		t.Fatalf("gateway calls = %d, want 1", got)
	}
}

// A request that already left the wire is never replayed, even without an
// answer: the gateway may have accepted it and a second copy would double it.
func TestChatDoesNotReplayARequestTheGatewayAlreadyReceived(t *testing.T) {
	var served atomic.Int32
	client, _ := proxiedClient(t, 0, func(writer http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		dropProxyConnection(writer)
	})

	if _, err := client.Chat(context.Background(), runtimeconfig.Snapshot{}, []byte(`{"model":"model-free"}`), false); err == nil {
		t.Fatal("Chat succeeded although the gateway hung up")
	}
	if got := served.Load(); got != 1 {
		t.Fatalf("gateway calls = %d, want 1 (a written request must not be replayed)", got)
	}
}

// A disabled pool must fail loudly instead of falling back to a direct dial that
// would put the router's own address on the wire.
func TestDisabledProxyNeverFallsBackToADirectDial(t *testing.T) {
	baseURL, err := url.Parse("http://opencodefree.invalid/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	client, err := NewClient(&http.Client{}, baseURL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client = client.WithProxy(disabledProxyProvider{})

	var proxyErr *xkproxy.Error
	if _, err := client.Models(context.Background()); !errors.As(err, &proxyErr) {
		t.Fatalf("error = %v, want an xkproxy error", err)
	}
}

type disabledProxyProvider struct{}

func (disabledProxyProvider) Configured() bool { return true }

func (disabledProxyProvider) Enabled() bool { return false }

func (disabledProxyProvider) Acquire(context.Context, runtimeconfig.Snapshot, string) (*xkproxy.Handle, error) {
	return nil, errors.New("acquire must not be reached while the pool is disabled")
}

func dropProxyConnection(writer http.ResponseWriter) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}
