package xkproxy

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestExternalPoolStatusChecksOnlyStandardHealthEndpoint(t *testing.T) {
	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	proxyURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(proxyURL, "runtime-proxy-secret", http.DefaultTransport.(*http.Transport), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	status := manager.PoolStatus()
	if status.Mode != "external" || !status.Configured || !status.Reachable {
		t.Fatalf("external status = %#v, want configured/reachable external mode", status)
	}
	if status.Endpoint != server.URL {
		t.Fatalf("endpoint = %q, want credential-free proxy URL %q", status.Endpoint, server.URL)
	}
	select {
	case path := <-paths:
		if path != "/healthz" {
			t.Fatalf("health probe path = %q, want /healthz", path)
		}
	default:
		t.Fatal("external status did not issue a health probe")
	}
}
