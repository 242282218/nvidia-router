package xkproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"nvidia-router/internal/runtimeconfig"
)

func TestSwitcherKeepsOldHandleUsableAfterReplacement(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy-pool-a:8080")
	if err != nil {
		t.Fatalf("url.Parse first proxy: %v", err)
	}
	first, err := New(proxyURL, "first-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New first manager: %v", err)
	}
	secondURL, err := url.Parse("http://proxy-pool-b:8080")
	if err != nil {
		t.Fatalf("url.Parse second proxy: %v", err)
	}
	second, err := New(secondURL, "second-secret", http.DefaultTransport.(*http.Transport), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New second manager: %v", err)
	}
	switcher := NewSwitcher(first, true)
	oldHandle, err := switcher.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000}, "")
	if err != nil {
		t.Fatalf("Acquire old handle: %v", err)
	}
	if err := switcher.Apply(second, true); err != nil {
		t.Fatalf("Apply replacement: %v", err)
	}
	newHandle, err := switcher.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 1000}, "")
	if err != nil {
		t.Fatalf("Acquire new handle: %v", err)
	}
	if oldHandle.Transport() == newHandle.Transport() {
		t.Fatal("replacement reused old transport")
	}
	if oldHandle.Transport() == nil {
		t.Fatal("old handle lost its transport")
	}
	oldHandle.Release()
	newHandle.Release()
	switcher.Close()
}

func TestSwitcherDisabledModeIsDirectAndCloseRejectsAcquire(t *testing.T) {
	switcher := NewSwitcher(nil, false)
	if switcher.Configured() || switcher.Enabled() {
		t.Fatal("disabled switcher reports proxy enabled")
	}
	switcher.Close()
	if _, err := switcher.Acquire(context.Background(), runtimeconfig.Snapshot{}, ""); err == nil {
		t.Fatal("closed switcher accepted Acquire")
	}
}
