package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/eventhub"
	"nvidia-router/internal/observability"
)

func TestRequestEventLineEncodesMetadataOnly(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 30, 0, 0, time.UTC)
	firstToken := int64(250)
	record := observability.RequestRecord{
		RequestID: "req-1", Endpoint: "/v1/chat/completions", ModelID: "deepseek-v4-flash",
		HTTPStatus: 200, Outcome: "success", IsStream: true,
		QueueMS: 12, FirstTokenMS: &firstToken, DurationMS: 900, CreatedAt: now,
	}
	line := RequestEventLine(record)
	if line == "" {
		t.Fatalf("empty event line")
	}
	if line[:len("event: request")] != "event: request" {
		t.Fatalf("event line lacks type prefix: %q", line[:len("event: request")])
	}
	for _, forbidden := range []string{`"body"`, `"message"`, `"prompt_tokens"`, `"completion_tokens"`} {
		if contains(line, forbidden) {
			t.Fatalf("event line leaked forbidden field %q: %s", forbidden, line)
		}
	}
}

// TestEventStreamReplaysRingThenLiveEvents runs the streaming handler against a
// cancellable context: the handler blocks until the client disconnects, which
// is exactly the long-lived SSE contract the live view depends on.
func TestEventStreamReplaysRingThenLiveEvents(t *testing.T) {
	hub := eventhub.New(5)
	hub.Publish(eventhub.Event{Type: "request", Serialized: "a"})

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/admin/api/events/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		NewEventStream(hub).ServeHTTP(response, request)
		close(done)
	}()

	// Wait for the replay to hit the recorder. httptest.ResponseRecorder is not
	// safe for concurrent read (Body.String) and write (handler writing), so
	// wait for the handler to complete before reading the body. The replayed
	// event should appear quickly; if not, the timeout will fire.
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		// Peek at length without reading the buffer to avoid the race. The
		// replayed event "a" plus SSE framing is at least 10 bytes.
		if response.Body.Len() > 10 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("stream did not emit replayed event within timeout")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not return after client disconnect")
	}
	wg.Wait()

	// Now safe to read the body after the handler goroutine has exited.
	body := response.Body.String()
	if !contains(body, "a") {
		t.Fatalf("stream body missing replayed event: %q", body)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content type = %q", contentType)
	}
}

func TestEventStreamRejectsWrongMethodAndPath(t *testing.T) {
	handler := NewEventStream(eventhub.New(5))
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/admin/api/events/stream"},
		{http.MethodGet, "/admin/api/events/stream/other"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(target.method, target.path, nil)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", target.method, target.path, response.Code)
		}
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
