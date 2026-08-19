package opencodefree

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nvidia-router/internal/runtimeconfig"
)

func TestModelsPrioritizeFreeEntriesAndPreserveOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer local-entry-key" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = io.WriteString(writer, `{"data":[{"id":"regular-a"},{"id":"model-free"},{"id":"regular-b"},{"id":"MODEL-FREE"},{"id":"regular-a"}]}`)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL + "/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	client, err := NewClient(server.Client(), baseURL, "local-entry-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	want := []string{"model-free", "MODEL-FREE", "regular-a", "regular-b"}
	if strings.Join(models, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("models = %v, want %v", models, want)
	}
}

func TestChatOmitsOptionalEntryAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization = %q, want empty", got)
		}
		if got := request.Header.Get("x-opencode-client"); got != "desktop" {
			t.Fatalf("x-opencode-client = %q", got)
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL + "/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	client, err := NewClient(server.Client(), baseURL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := client.Chat(context.Background(), runtimeconfig.Snapshot{}, []byte(`{"model":"model-free"}`), false)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
}
