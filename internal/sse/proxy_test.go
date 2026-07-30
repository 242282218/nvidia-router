package sse

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/router"
)

func TestProxyPassthroughPreservesCommentsAndNonJSONData(t *testing.T) {
	input := ": keep-alive\ndata: non-json-value\n\ndata: [DONE]\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	commit := &router.CommitState{}
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{CommitState: commit})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, ": keep-alive") {
		t.Fatalf("comment not preserved: %s", body)
	}
	if !strings.Contains(body, "data: non-json-value") {
		t.Fatalf("non-JSON data not preserved: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("[DONE] not in response: %s", body)
	}
}

func TestProxyDeduplicatesDONE(t *testing.T) {
	input := "data: first\n\ndata: [DONE]\n\ndata: [DONE]\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	body := recorder.Body.String()
	count := strings.Count(body, "[DONE]")
	if count != 1 {
		t.Fatalf("[DONE] count = %d, want 1; body = %s", count, body)
	}
}

func TestProxyReturnsStreamInterruptedOnEarlyEOF(t *testing.T) {
	input := "data: first event\n\ndata: truncated"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{})
	if err != ErrStreamInterrupted {
		t.Fatalf("expected ErrStreamInterrupted, got: %v", err)
	}
}

func TestProxyIgnoresEventsAfterDONE(t *testing.T) {
	input := "data: [DONE]\n\ndata: should-be-ignored\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "should-be-ignored") {
		t.Fatalf("post-DONE event leaked: %s", body)
	}
}

func TestProxyCancelOnContextDone(t *testing.T) {
	// Infinite stream - should cancel via context
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for {
			_, err := pw.Write([]byte("data: chunk\n\n"))
			if err != nil {
				return
			}
		}
	}()
	upstream := &http.Response{Body: pr}

	ctx, cancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()

	done := make(chan error, 1)
	go func() {
		done <- Proxy(ctx, recorder, upstream, ProxyOptions{})
	}()

	// Read first chunk then cancel
	cancel()
	err := <-done
	if err == nil || err == ErrStreamInterrupted {
		t.Fatalf("expected context cancellation error, got: %v", err)
	}
}

func TestProxyCommitStateSetOnFirstEvent(t *testing.T) {
	input := "data: first-real-event\n\ndata: [DONE]\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	commit := &router.CommitState{}
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{CommitState: commit})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if !commit.Committed() {
		t.Fatal("commit state not set after writing first event")
	}
}
