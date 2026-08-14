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

func TestPrimeReplayBodyForwardsSemanticCompletion(t *testing.T) {
	body := &markableReadCloser{Reader: strings.NewReader("data: payload\n\n")}
	response := &http.Response{Body: body}

	if err := Prime(context.Background(), response); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if !body.semanticRequired {
		t.Fatal("Prime did not require semantic completion")
	}
	marker, ok := response.Body.(interface{ MarkComplete() })
	if !ok {
		t.Fatal("primed body does not expose MarkComplete")
	}
	marker.MarkComplete()
	if !body.completed {
		t.Fatal("MarkComplete was not forwarded to the original body")
	}
	_ = response.Body.Close()
}

func TestPrimeReplayBodyKeepsSemanticCompletionRequired(t *testing.T) {
	body := &markableReadCloser{Reader: strings.NewReader("data: payload\n\n")}
	response := &http.Response{Body: body}

	if err := Prime(context.Background(), response); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if marker, ok := response.Body.(interface{ RequireSemanticCompletion() }); ok {
		marker.RequireSemanticCompletion()
	} else {
		t.Fatal("primed body does not expose RequireSemanticCompletion")
	}
	if !body.semanticRequired {
		t.Fatal("semantic completion requirement did not reach original body")
	}
	_ = response.Body.Close()
}

func TestPrimeRequiresSemanticCompletionBeforeDataAndEOFRead(t *testing.T) {
	body := &markableReadCloser{Reader: strings.NewReader("data: payload\n\n")}
	response := &http.Response{Body: body}

	if err := Prime(context.Background(), response); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if !body.semanticRequired {
		t.Fatal("Prime required semantic completion too late for a data-and-EOF read")
	}
	if body.eofBeforeSemantic {
		t.Fatal("underlying body observed EOF before Prime required semantic completion")
	}
	_ = response.Body.Close()
}

func TestPrimeCommentOnlyEOFReturnsInterrupted(t *testing.T) {
	body := &markableReadCloser{Reader: strings.NewReader(": keep-alive\n\n")}
	response := &http.Response{Body: body}

	err := Prime(context.Background(), response)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Prime error = %v, want io.ErrUnexpectedEOF", err)
	}
	if !body.semanticRequired {
		t.Fatal("Prime did not require semantic completion when the stream ended before its first data event")
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

func TestProxyMarksUnderlyingBodyCompleteAfterDONE(t *testing.T) {
	var completed atomic.Int32
	body := io.NopCloser(strings.NewReader("data: [DONE]\n\n"))
	upstream := &http.Response{Body: body}
	if err := Proxy(context.Background(), httptest.NewRecorder(), upstream, ProxyOptions{OnComplete: func() { completed.Add(1) }}); err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if completed.Load() != 1 {
		t.Fatalf("completion callback count = %d, want 1", completed.Load())
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

func TestWriteWatchdogFiresOnStalledWriteAndRunsCallback(t *testing.T) {
	// Audit H6: a flush blocked on a client that stopped reading must trip the
	// watchdog, run its onStall callback (close upstream body), and report Fired.
	var stallRan atomic.Bool
	watchdog := NewWriteWatchdog(30*time.Millisecond, func() { stallRan.Store(true) })
	defer watchdog.Stop()

	watchdog.Arm()
	// Without a Disarm the timer fires on its own; give it time to run.
	deadline := time.Now().Add(200 * time.Millisecond)
	for !stallRan.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !stallRan.Load() {
		t.Fatal("onStall did not run after the idle window elapsed")
	}
	if !watchdog.Fired() {
		t.Fatal("watchdog not marked fired after stall")
	}
}

func TestWriteWatchdogDisarmPreventsStall(t *testing.T) {
	// A flush that completes before the window must not trip the watchdog.
	var stallRan atomic.Bool
	watchdog := NewWriteWatchdog(30*time.Millisecond, func() { stallRan.Store(true) })
	defer watchdog.Stop()

	watchdog.Arm()
	watchdog.Disarm()
	time.Sleep(60 * time.Millisecond)
	if stallRan.Load() {
		t.Fatal("onStall ran after a prompt Disarm")
	}
	if watchdog.Fired() {
		t.Fatal("watchdog marked fired despite prompt Disarm")
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

type markableReadCloser struct {
	*strings.Reader
	completed         bool
	semanticRequired  bool
	eofBeforeSemantic bool
}

func (r *markableReadCloser) Close() error { return nil }

func (r *markableReadCloser) Read(payload []byte) (int, error) {
	read, err := r.Reader.Read(payload)
	if err == io.EOF && !r.semanticRequired {
		r.eofBeforeSemantic = true
	}
	return read, err
}

func (r *markableReadCloser) RequireSemanticCompletion() { r.semanticRequired = true }

func (r *markableReadCloser) MarkComplete() { r.completed = true }

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

func TestProxyWriteWatchdogInterruptsBlockedFlush(t *testing.T) {
	upstream := &http.Response{Body: io.NopCloser(strings.NewReader("data: first\n\n"))}
	writer := newBlockingFlushWriter()
	done := make(chan error, 1)
	go func() {
		done <- Proxy(context.Background(), writer, upstream, ProxyOptions{WriteIdleTimeout: 20 * time.Millisecond})
	}()

	select {
	case <-writer.flushStarted:
	case <-time.After(time.Second):
		t.Fatal("Flush did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamWriteStalled) {
			t.Fatalf("Proxy error = %v, want ErrStreamWriteStalled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Proxy remained blocked in Flush after watchdog timeout")
	}
}

type blockingFlushWriter struct {
	*httptest.ResponseRecorder
	flushStarted chan struct{}
	unblock      chan struct{}
	unblockOnce  sync.Once
}

func newBlockingFlushWriter() *blockingFlushWriter {
	return &blockingFlushWriter{
		ResponseRecorder: httptest.NewRecorder(),
		flushStarted:     make(chan struct{}),
		unblock:          make(chan struct{}),
	}
}

func (w *blockingFlushWriter) Flush() {
	close(w.flushStarted)
	<-w.unblock
}

func (w *blockingFlushWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		wait := time.Until(deadline)
		if wait < 0 {
			wait = 0
		}
		time.AfterFunc(wait, func() {
			w.unblockOnce.Do(func() { close(w.unblock) })
		})
	}
	return nil
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
