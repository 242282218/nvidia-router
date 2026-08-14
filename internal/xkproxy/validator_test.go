package xkproxy

import (
	"context"
	"testing"
	"time"
)

// TestValidatorTransportEnablesHTTP2 guards the same h2 setting on the validation
// probe that the request path (manager.go) carries. The validation target sits
// behind a CDN that speaks HTTP/2; disabling h2 made every fetched proxy fail
// validation with a malformed-response error and drained the pool to stale
// last-known-good exits (found in real 联调 2026-08-13).
func TestValidatorTransportEnablesHTTP2(t *testing.T) {
	v := NewValidator("https://integrate.api.nvidia.com/v1", 404, 5*time.Second)
	t.Cleanup(v.Close)

	proxy := Proxy{Scheme: "http", Address: "10.0.0.1:8080"}
	transport := v.transportFor(proxy, "http://10.0.0.1:8080", 2*time.Second)
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("validation transport must enable HTTP/2 for the tunnel target")
	}
}

// TestValidatorRejectsMissingHost keeps the probe's address validation honest: an
// exit without a host must fail fast rather than produce an opaque transport error.
func TestValidatorRejectsMissingHost(t *testing.T) {
	v := NewValidator("https://integrate.api.nvidia.com/v1", 404, 5*time.Second)
	t.Cleanup(v.Close)

	err := v.Validate(context.Background(), Proxy{Scheme: "http"})
	if err == nil {
		t.Fatal("Validate accepted a proxy without a host")
	}
}
