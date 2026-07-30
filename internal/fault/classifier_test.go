package fault

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClassifierResponseTable(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		status     int
		models     bool
		body       string
		retryAfter string
		scope      Scope
		retryable  bool
		disableKey bool
		blockModel bool
		wantRetry  time.Duration
	}{
		{name: "request 400", status: 400, scope: ScopeRequest},
		{name: "request 404", status: 404, scope: ScopeRequest},
		{name: "request 409", status: 409, scope: ScopeRequest},
		{name: "request 422", status: 422, scope: ScopeRequest},
		{name: "explicit invalid key on 400", status: 400, body: `{"error":{"code":"invalid_api_key"}}`, scope: ScopeCredential, retryable: true, disableKey: true},
		{name: "unauthorized", status: 401, scope: ScopeCredential, retryable: true, disableKey: true},
		{name: "models forbidden", status: 403, models: true, scope: ScopeCredential, retryable: true, disableKey: true},
		{name: "model forbidden", status: 403, scope: ScopeModelCredential, retryable: true, blockModel: true},
		{name: "explicit invalid key", status: 403, body: `{"error":{"code":"invalid_api_key"}}`, scope: ScopeCredential, retryable: true, disableKey: true},
		{name: "explicit auth type", status: 403, body: `{"error":{"type":"authentication_error"}}`, scope: ScopeCredential, retryable: true, disableKey: true},
		{name: "account deactivated", status: 403, body: `{"error":{"code":"account_deactivated"}}`, scope: ScopeCredential, retryable: true, disableKey: true},
		{name: "rate limited", status: 429, retryAfter: "7", scope: ScopeTransientCredential, retryable: true, wantRetry: 7 * time.Second},
		{name: "server 500", status: 500, scope: ScopeUpstreamGlobal, retryable: true},
		{name: "server 502", status: 502, scope: ScopeUpstreamGlobal, retryable: true},
		{name: "server 503", status: 503, scope: ScopeUpstreamGlobal, retryable: true},
		{name: "server 504", status: 504, scope: ScopeUpstreamGlobal, retryable: true},
		{name: "other 5xx", status: 501, scope: ScopeUpstreamGlobal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := responseForClassifier(tt.status, tt.body, tt.retryAfter)
			got := Classify(response, nil, tt.models, now)
			if got.Scope != tt.scope || got.Retryable != tt.retryable || got.DisableKey != tt.disableKey || got.BlockModel != tt.blockModel {
				t.Fatalf("Classify() = %+v", got)
			}
			if got.RetryAfter != tt.wantRetry {
				t.Fatalf("RetryAfter = %s, want %s", got.RetryAfter, tt.wantRetry)
			}
		})
	}
}

func TestClassifierNetworkErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
		code      string
	}{
		{name: "deadline", err: context.DeadlineExceeded, retryable: true, code: "upstream_timeout"},
		{name: "reset", err: &url.Error{Op: "Post", URL: "https://example.invalid", Err: syscall.ECONNRESET}, retryable: true, code: "upstream_connection_error"},
		{name: "DNS", err: &net.DNSError{Err: "temporary DNS failure", Name: "example.invalid", IsTemporary: true}, retryable: true, code: "upstream_connection_error"},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, retryable: true, code: "upstream_connection_error"},
		{name: "canceled", err: context.Canceled, code: "request_canceled"},
		{name: "unknown", err: errors.New("unknown local failure"), code: "upstream_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(nil, tt.err, false, time.Time{})
			if got.Retryable != tt.retryable || got.PublicCode != tt.code {
				t.Fatalf("Classify() = %+v", got)
			}
		})
	}
}

func TestClassifierPreservesRetryableProtocolFault(t *testing.T) {
	cause := errors.New("malformed upstream response")
	got := Classify(nil, Protocol(cause), false, time.Time{})
	if got.HTTPStatus != http.StatusBadGateway || got.Scope != ScopeUpstreamGlobal || !got.Retryable {
		t.Fatalf("Classify() = %+v", got)
	}
	if got.PublicCode != "upstream_protocol_error" || !errors.Is(got, cause) {
		t.Fatalf("Classify() = %+v, cause = %v", got, cause)
	}
}

func TestClassifierIgnoresMessageAndUnsafeSummaryValues(t *testing.T) {
	secret := "Bearer nvapi-secret"
	for _, body := range []string{
		`{"error":{"message":"invalid_api_key ` + secret + `"}}`,
		`{"error":{"code":"invalid api key","type":"authentication error","message":"` + secret + `"}}`,
		`not-json ` + secret,
	} {
		got := Classify(responseForClassifier(403, body, ""), nil, false, time.Time{})
		if got.DisableKey || !got.BlockModel {
			t.Fatalf("unsafe body changed conservative classification: %+v", got)
		}
		for _, value := range []string{got.PublicCode, got.PublicType, got.PublicMessage, got.Error()} {
			if strings.Contains(value, secret) || strings.Contains(value, "invalid api key") {
				t.Fatalf("classification leaked upstream body: %q", value)
			}
		}
	}
}

func responseForClassifier(status int, body, retryAfter string) *http.Response {
	header := make(http.Header)
	if retryAfter != "" {
		header.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}
