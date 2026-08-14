package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	response := &readyDeadlineRecorder{deadlineRecorder: deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, ready: make(chan struct{})}

	done := make(chan struct{})
	go func() {
		NewEventStream(hub).ServeHTTP(response, request)
		close(done)
	}()

	// The writer signals after the heartbeat write, so cancellation only occurs
	// after the handler has acquired its slot and entered the live stream.
	<-response.ready
	cancel()
	<-done

	// Safe to read after handler exits.
	if contentType := response.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content type = %q", contentType)
	}
	// Verify the handler wrote something (at least the ": connected\n\n" heartbeat).
	if response.Body.Len() == 0 {
		t.Fatal("stream wrote no data")
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

func TestEventStreamRejectsConnectionWhenSubscriberLimitReached(t *testing.T) {
	hub := eventhub.New(5)
	first := NewEventStream(hub, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstReady := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		first.ServeHTTP(&readyDeadlineRecorder{deadlineRecorder: deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, ready: firstReady}, httptest.NewRequest(http.MethodGet, "/admin/api/events/stream", nil).WithContext(ctx))
		close(firstDone)
	}()
	response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	<-firstReady
	first.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/events/stream", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	cancel()
	<-firstDone
}

func TestEventStreamUsesEarlierRequestDeadline(t *testing.T) {
	hub := eventhub.New(1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	writer := &recordingDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	NewEventStream(hub, 1).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/admin/api/events/stream", nil).WithContext(ctx))
	if writer.firstDeadline.IsZero() || time.Until(writer.firstDeadline) > 200*time.Millisecond {
		t.Fatalf("first write deadline = %v, want request deadline", writer.firstDeadline)
	}
}

func TestEventStreamExpiredRequestDeadlineIsImmediate(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if got := eventStreamWriteTimeoutFor(ctx); got <= 0 || got > time.Millisecond {
		t.Fatalf("expired event write timeout = %s, want immediate positive timeout", got)
	}
}

type deadlineRecorder struct{ *httptest.ResponseRecorder }

func (*deadlineRecorder) SetWriteDeadline(time.Time) error { return nil }

type recordingDeadlineRecorder struct {
	*httptest.ResponseRecorder
	firstDeadline time.Time
}

func (w *recordingDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && w.firstDeadline.IsZero() {
		w.firstDeadline = deadline
	}
	return nil
}

type readyDeadlineRecorder struct {
	deadlineRecorder
	ready chan struct{}
}

func (w *readyDeadlineRecorder) Write(payload []byte) (int, error) {
	select {
	case w.ready <- struct{}{}:
	default:
	}
	return w.ResponseRecorder.Write(payload)
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
