package sse

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestPrimeWaitsPastCommentAndReplaysThroughFirstDataEvent(t *testing.T) {
	release := make(chan struct{})
	body := &stagedReadCloser{
		first:      []byte(": keep-alive\n\n"),
		second:     []byte("data: payload\n\n"),
		secondRead: make(chan struct{}),
		release:    release,
	}
	response := &http.Response{Body: body}
	done := make(chan error, 1)
	go func() {
		done <- Prime(context.Background(), response)
	}()

	select {
	case <-body.secondRead:
		close(release)
	case err := <-done:
		close(release)
		t.Fatalf("Prime returned before a data event: %v", err)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Prime did not continue reading after the comment")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Prime: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Prime did not return after the data event")
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read replayed body: %v", err)
	}
	if got, want := string(payload), ": keep-alive\n\ndata: payload\n\n"; got != want {
		t.Fatalf("replayed body = %q, want %q", got, want)
	}
}

func TestPrimeCommentOnlyEOFReturnsInterrupted(t *testing.T) {
	response := &http.Response{Body: io.NopCloser(strings.NewReader(": keep-alive\n\n"))}

	err := Prime(context.Background(), response)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Prime error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestProxyCommentOnlyDoesNotCommit(t *testing.T) {
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(": keep-alive\n\n"))}
	recorder := httptest.NewRecorder()
	commit := &router.CommitState{}

	err := Proxy(context.Background(), commit.Wrap(recorder), upstream, ProxyOptions{CommitState: commit})

	if !errors.Is(err, ErrStreamInterrupted) {
		t.Fatalf("Proxy error = %v, want ErrStreamInterrupted", err)
	}
	if commit.Committed() {
		t.Fatal("comment-only stream committed the response")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("comment-only response body = %q, want empty", recorder.Body.String())
	}
}

func TestProxyRejectsCumulativePendingCommentsBeforeCommit(t *testing.T) {
	commentEvent := ": keep-alive\n\n"
	input := strings.Repeat(commentEvent, MaxEventSize/len(commentEvent)+1)
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}
	recorder := httptest.NewRecorder()
	commit := &router.CommitState{}

	err := Proxy(context.Background(), commit.Wrap(recorder), upstream, ProxyOptions{CommitState: commit})

	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("Proxy error = %v, want ErrEventTooLarge", err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("response body = %q, want empty", recorder.Body.String())
	}
	if commit.Committed() {
		t.Fatal("comment-only stream committed the response")
	}
}

func TestPrimeCancellationClosesBodyAndWaitsForReadToExit(t *testing.T) {
	body := &cancelableReadCloser{
		started: make(chan struct{}),
		exited:  make(chan struct{}),
		release: make(chan struct{}),
	}
	response := &http.Response{Body: body}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Prime(ctx, response)
	}()

	select {
	case <-body.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("Prime did not start reading the body")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Prime error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Prime did not return after cancellation")
	}

	select {
	case <-body.exited:
	default:
		t.Fatal("Prime returned before the body read exited")
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

func TestProxyTruncatesDataAfterDONEWithinSameEvent(t *testing.T) {
	// A malformed upstream packs data lines after the terminal [DONE] in one
	// event; those must not reach the client (checklist #53).
	input := "data: [DONE]\ndata: leaked-extra\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "leaked-extra") {
		t.Fatalf("post-[DONE] data line leaked: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("[DONE] marker not forwarded: %s", body)
	}
}

func TestPrimeRejectsCommentPreambleOverCaptureCap(t *testing.T) {
	// A stream of comment/keep-alive events before any data event must not grow
	// the Prime capture buffer without bound (checklist #13).
	upstream := &http.Response{
		Body: io.NopCloser(&repeatEventReader{event: []byte(": keep-alive\n\n"), left: MaxCaptureBytes + 1}),
	}
	err := Prime(context.Background(), upstream)
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("Prime error = %v, want ErrEventTooLarge", err)
	}
}

// repeatEventReader emits the same SSE event until left bytes are produced,
// letting tests stream a long preamble without allocating it up front.
type repeatEventReader struct {
	event []byte
	left  int64
}

func (r *repeatEventReader) Read(payload []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := copy(payload, r.event)
	if int64(n) > r.left {
		n = int(r.left)
	}
	r.left -= int64(n)
	return n, nil
}

func TestProxyCancelOnContextDone(t *testing.T) {
	// Infinite stream - should cancel via context
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
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

func TestProxyCommitStateSetOnFirstDataEvent(t *testing.T) {
	input := ": keep-alive\n\ndata: first-real-event\n\ndata: [DONE]\n\n"
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
	if !strings.Contains(recorder.Body.String(), ": keep-alive") {
		t.Fatalf("leading comment was not preserved: %q", recorder.Body.String())
	}
}

func TestProxyOnFirstDataFiresOnceAfterFirstDataEvent(t *testing.T) {
	input := ": keep-alive\n\ndata: first-event\n\ndata: second-event\n\ndata: [DONE]\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	var calls atomic.Int32
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{OnFirstData: func() { calls.Add(1) }})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("OnFirstData calls = %d, want exactly 1", calls.Load())
	}
}

func TestProxyOnFirstDataNotFiredForCommentOnlyStream(t *testing.T) {
	input := ": keep-alive\n\n: keep-alive-2\n\n"
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader(input))}

	recorder := httptest.NewRecorder()
	var calls int
	err := Proxy(context.Background(), recorder, upstream, ProxyOptions{OnFirstData: func() { calls++ }})
	if !errors.Is(err, ErrStreamInterrupted) {
		t.Fatalf("Proxy error = %v, want ErrStreamInterrupted", err)
	}
	if calls != 0 {
		t.Fatalf("OnFirstData calls = %d, want 0 for a comment-only stream", calls)
	}
}

type stagedReadCloser struct {
	first      []byte
	second     []byte
	reads      int
	secondRead chan struct{}
	release    <-chan struct{}
}

func (r *stagedReadCloser) Read(payload []byte) (int, error) {
	switch r.reads {
	case 0:
		r.reads++
		return copy(payload, r.first), nil
	case 1:
		r.reads++
		close(r.secondRead)
		<-r.release
		return copy(payload, r.second), nil
	default:
		return 0, io.EOF
	}
}

func (*stagedReadCloser) Close() error { return nil }

type cancelableReadCloser struct {
	started chan struct{}
	exited  chan struct{}
	release chan struct{}
	start   sync.Once
	close   sync.Once
}

func (r *cancelableReadCloser) Read([]byte) (int, error) {
	r.start.Do(func() { close(r.started) })
	defer close(r.exited)
	<-r.release
	return 0, io.ErrClosedPipe
}

func (r *cancelableReadCloser) Close() error {
	r.start.Do(func() { close(r.started) })
	r.close.Do(func() { close(r.release) })
	return nil
}
