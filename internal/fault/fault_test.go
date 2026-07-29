package fault

import (
	"errors"
	"strings"
	"testing"
)

func TestScopeValuesRemainStable(t *testing.T) {
	if ScopeRequest != 0 {
		t.Fatalf("ScopeRequest = %d, want 0", ScopeRequest)
	}
	if ScopeCredential != 1 {
		t.Fatalf("ScopeCredential = %d, want 1", ScopeCredential)
	}
	if ScopeModelCredential != 2 {
		t.Fatalf("ScopeModelCredential = %d, want 2", ScopeModelCredential)
	}
	if ScopeTransientCredential != 3 {
		t.Fatalf("ScopeTransientCredential = %d, want 3", ScopeTransientCredential)
	}
	if ScopeUpstreamGlobal != 4 {
		t.Fatalf("ScopeUpstreamGlobal = %d, want 4", ScopeUpstreamGlobal)
	}
}

func TestNewSetsRequiredPublicFaultFields(t *testing.T) {
	cause := errors.New("Authorization: Bearer nvapi-secret internal stack")

	got := New(429, ScopeCredential, "rate_limit_error", "rate_limit_exceeded", "Please retry later.", cause)

	if got.HTTPStatus != 429 {
		t.Fatalf("HTTPStatus = %d, want 429", got.HTTPStatus)
	}
	if got.Scope != ScopeCredential {
		t.Fatalf("Scope = %d, want %d", got.Scope, ScopeCredential)
	}
	if got.PublicType != "rate_limit_error" {
		t.Fatalf("PublicType = %q", got.PublicType)
	}
	if got.PublicCode != "rate_limit_exceeded" {
		t.Fatalf("PublicCode = %q", got.PublicCode)
	}
	if got.PublicMessage != "Please retry later." {
		t.Fatalf("PublicMessage = %q", got.PublicMessage)
	}
	if got.Cause != cause {
		t.Fatalf("Cause = %v, want %v", got.Cause, cause)
	}
	if got.Retryable || got.RetryAfter != 0 || got.DisableKey || got.BlockModel {
		t.Fatalf("classification fields were initialized: %+v", got)
	}
}

func TestFaultErrorDoesNotLeakCause(t *testing.T) {
	fault := New(
		500,
		ScopeUpstreamGlobal,
		"server_error",
		"upstream_failure",
		"The upstream service failed.",
		errors.New("Authorization: Bearer nvapi-secret https://internal.example.invalid stack: upstreamCall"),
	)

	message := fault.Error()
	if message == "" {
		t.Fatal("Error() returned empty message")
	}
	for _, sensitive := range []string{"nvapi-secret", "internal.example.invalid", "upstreamCall"} {
		if strings.Contains(message, sensitive) {
			t.Fatalf("Error() leaked %q: %s", sensitive, message)
		}
	}
}

func TestFaultUnwrapSupportsErrorsIs(t *testing.T) {
	cause := errors.New("upstream unavailable")
	fault := New(503, ScopeUpstreamGlobal, "server_error", "upstream_unavailable", "Try again later.", cause)

	if !errors.Is(fault, cause) {
		t.Fatalf("errors.Is(%v, %v) = false, want true", fault, cause)
	}
	if unwrapped := fault.Unwrap(); unwrapped != cause {
		t.Fatalf("Unwrap() = %v, want %v", unwrapped, cause)
	}
}
