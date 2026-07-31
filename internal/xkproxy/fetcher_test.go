package xkproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetcherAcceptsSingleIPPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}
		_, _ = io.WriteString(writer, "  192.0.2.10:8080\r\n")
	}))
	t.Cleanup(server.Close)

	proxyURL := fetcherURL(t, server.URL)
	got, err := newFetcher(proxyURL).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Scheme != "http" || got.Host != "192.0.2.10:8080" {
		t.Fatalf("proxy URL = %q", got)
	}
}

func TestFetcherAcceptsIPv6AndMaximumPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "[2001:db8::10]:65535")
	}))
	t.Cleanup(server.Close)

	got, err := newFetcher(fetcherURL(t, server.URL)).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Scheme != "http" || got.Host != "[2001:db8::10]:65535" {
		t.Fatalf("proxy URL = %q", got)
	}
}

func TestFetcherDoesNotUseProcessProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "192.0.2.10:8080")
	}))
	t.Cleanup(server.Close)

	got, err := newFetcher(fetcherURL(t, server.URL)).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Host != "192.0.2.10:8080" {
		t.Fatalf("proxy URL = %q", got)
	}
}

func TestFetcherRejectsInvalidResponsesWithoutLeakingDetails(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
	}{
		{name: "empty", code: http.StatusOK, body: ""},
		{name: "multiple lines", code: http.StatusOK, body: "192.0.2.10:80\n192.0.2.11:80"},
		{name: "host name", code: http.StatusOK, body: "proxy.example.test:80"},
		{name: "port zero", code: http.StatusOK, body: "192.0.2.10:0"},
		{name: "port overflow", code: http.StatusOK, body: "192.0.2.10:65536"},
		{name: "HTML", code: http.StatusOK, body: "<html>private response</html>"},
		{name: "non-2xx", code: http.StatusBadGateway, body: "private response"},
		{name: "too large", code: http.StatusOK, body: strings.Repeat("x", 4097)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.code)
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)
			apiURL := fetcherURL(t, server.URL)
			apiURL.RawQuery = "apikey=secret-key&sign=secret-sign"

			_, err := newFetcher(apiURL).Fetch(context.Background())
			if err == nil {
				t.Fatal("Fetch succeeded")
			}
			var proxyErr *Error
			if !errors.As(err, &proxyErr) {
				t.Fatalf("error = %T %v, want *Error", err, err)
			}
			for _, secret := range []string{server.URL, test.body, "192.0.2.10:80", "secret-key", "secret-sign"} {
				if secret == "" {
					continue
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestFetcherHonorsParentCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := newFetcher(fetcherURL(t, server.URL)).Fetch(ctx)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fetch error = %v, want context deadline", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetch request did not start")
	}
}

func fetcherURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
