package opencodefree

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

func TestIsLocalHostClassification(t *testing.T) {
	local := []string{
		"", "localhost", "gateway.localhost", "127.0.0.1", "::1", "0.0.0.0",
		"10.1.2.3", "172.16.0.9", "192.168.1.5", "169.254.10.10",
		"opencode-free-proxy-opencode-free-proxy-1",
	}
	for _, host := range local {
		if !isLocalHost(host) {
			t.Fatalf("host %q must be treated as local", host)
		}
	}
	for _, host := range []string{"gateway.example.com", "8.8.8.8", "api.opencode.ai"} {
		if isLocalHost(host) {
			t.Fatalf("host %q must be treated as remote", host)
		}
	}
}

// refusingProxy fails every acquire, so any attempt to route through the pool
// turns into a visible error instead of a silent fallback.
type refusingProxy struct{}

func (refusingProxy) Configured() bool { return true }
func (refusingProxy) Enabled() bool    { return true }
func (refusingProxy) Acquire(context.Context, runtimeconfig.Snapshot, string) (*xkproxy.Handle, error) {
	return nil, xkproxy.NewTransportError(errUnreachable)
}

var errUnreachable = &unreachableError{}

type unreachableError struct{}

func (*unreachableError) Error() string { return "exit cannot route to a private address" }

func TestLocalGatewayBypassesProxyPool(t *testing.T) {
	// A container-internal gateway is unreachable from any exit; routing it
	// through the pool is what turned a healthy gateway into a 502.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"muse-spark-1.2-contributor-free"}]}`))
	}))
	t.Cleanup(server.Close)

	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	client, err := NewClient(server.Client(), baseURL, "key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client = client.WithProxy(refusingProxy{})

	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models through a local gateway must bypass the proxy pool: %v", err)
	}
	if len(models) != 1 || models[0] != "muse-spark-1.2-contributor-free" {
		t.Fatalf("models = %v", models)
	}
}
