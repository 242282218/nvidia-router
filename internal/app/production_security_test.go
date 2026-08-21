package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"nvidia-router/internal/config"
)

// The startup audit reads config.ListenAddress, which is always a host:port
// string. Comparing that whole value against bare hosts made every loopback
// deployment emit three false SECURITY warnings and let the bare ":port"
// wildcard form skip the public-binding warning entirely.
func TestListenExposureClassifiesHostPortAddresses(t *testing.T) {
	cases := []struct {
		address  string
		loopback bool
		wildcard bool
	}{
		{"127.0.0.1:3756", true, false},
		{"127.0.0.1", true, false},
		{"localhost:3756", true, false},
		{"[::1]:3756", true, false},
		{"::1", true, false},
		{"0.0.0.0:3756", false, true},
		{"0.0.0.0", false, true},
		{"[::]:3756", false, true},
		{":3756", false, true},
		{"", false, true},
		{"10.0.0.5:3756", false, false},
		{"router.internal:3756", false, false},
	}
	for _, testCase := range cases {
		loopback, wildcard := listenExposure(testCase.address)
		if loopback != testCase.loopback || wildcard != testCase.wildcard {
			t.Errorf("listenExposure(%q) = (loopback %t, wildcard %t); want (%t, %t)",
				testCase.address, loopback, wildcard, testCase.loopback, testCase.wildcard)
		}
	}
}

func TestCheckProductionSecuritySilentForLoopbackListener(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// The compose default for local runs: loopback bound, no reverse proxy set up.
	checkProductionSecurity(config.Config{ListenAddress: "127.0.0.1:3756"}, logger)
	if strings.Contains(logs.String(), "SECURITY") {
		t.Fatalf("loopback listener must not raise the exposure audit, got: %s", logs.String())
	}
}

// config.Load always substitutes the default listen address, so an empty one only
// reaches the audit from a hand-built Config that serves through its own listener.
// Warning there is a false positive, and one of the warnings names the Cookie
// header, which trips the log-leak scan in tests/mocknvidia.
func TestCheckProductionSecuritySilentWithoutListenAddress(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	checkProductionSecurity(config.Config{}, logger)
	if logs.Len() != 0 {
		t.Fatalf("audit must stay silent when no listener is configured, got: %s", logs.String())
	}
}

func TestCheckProductionSecurityWarnsOnWildcardListeners(t *testing.T) {
	for _, address := range []string{"0.0.0.0:3756", ":3756", "[::]:3756"} {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		checkProductionSecurity(config.Config{ListenAddress: address}, logger)
		output := logs.String()
		if !strings.Contains(output, "Listening on all interfaces") {
			t.Errorf("listen %q must raise the public-binding warning, got: %s", address, output)
		}
		if !strings.Contains(output, "AdminSecureCookie is disabled") {
			t.Errorf("listen %q must raise the insecure-cookie warning, got: %s", address, output)
		}
	}
}

func TestCheckProductionSecurityWarnsOnRoutableHostWithoutPublicBindingNotice(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	checkProductionSecurity(config.Config{ListenAddress: "10.0.0.5:3756"}, logger)
	output := logs.String()
	if !strings.Contains(output, "AdminExternalOrigin is not configured") {
		t.Fatalf("routable listener must raise the origin warning, got: %s", output)
	}
	if strings.Contains(output, "Listening on all interfaces") {
		t.Fatalf("a single routable address is not a wildcard bind, got: %s", output)
	}
}
