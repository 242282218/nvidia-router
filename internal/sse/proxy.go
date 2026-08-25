package sse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"nvidia-router/internal/router"
)

var ErrStreamInterrupted = errors.New("upstream stream interrupted before [DONE]")
var ErrStreamEmptyResponse = errors.New("upstream stream completed without a user-visible answer")
var ErrWriteDeadlineUnsupported = errors.New("response writer does not support write deadlines")

// ErrStreamWriteStalled reports that the client stopped reading for the whole
// write-idle window, so TCP backpressure has pinned the flush. The handler
// treats it like any post-commit truncation: the client is not consuming, so
// nothing more can be delivered, and the lease must be released (audit H6).
var ErrStreamWriteStalled = errors.New("upstream stream write stalled for too long")

type ProxyOptions struct {
	CommitState *router.CommitState
	// OnComplete fires after a valid terminal [DONE] marker is forwarded.
	OnComplete func()
	// BeforeComplete runs after all preceding data has been flushed but before
	// the terminal [DONE] marker is written. Returning an error aborts the
	// terminal marker so the caller can emit an explicit in-stream failure.
	BeforeComplete func() error
	// OnFirstData fires exactly once, after the first SSE data event has been
	// written and flushed to the client. It lets the streaming handler record
	// time-to-first-token without coupling the proxy to observability.
	OnFirstData func()
	// OnData fires for every SSE data event, after it is flushed to the client,
	// with the event's joined data payload. It runs on the proxy decode-loop
	// goroutine and must not block; [DONE] is delivered through OnComplete, not
	// here. Comment/pending events with no data frames never reach it. Streaming
	// handlers use it for reasoning-length sampling.
	OnData func(string)
	// WriteIdleTimeout bounds how long a flush may stay blocked on the client's
	// TCP receive buffer before the stream is torn down and the lease released.
	// A slow/stalled consumer would otherwise pin the credential slot until the
	// client disconnects (the request context only fires on disconnect, not on a
	// connected-but-not-reading client). Zero disables the write watchdog.
	WriteIdleTimeout time.Duration
}

func Proxy(ctx context.Context, writer http.ResponseWriter, upstream *http.Response, opts ProxyOptions) error {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return errors.New("response writer does not support flushing")
	}

	// Close upstream body when the client disconnects or the context is cancelled.
	// Request contexts cancel on client close, covering both HTTP/1.1 and HTTP/2.
	cancelDone := make(chan struct{})
	defer close(cancelDone)

	go func() {
		select {
		case <-ctx.Done():
			_ = upstream.Body.Close()
		case <-cancelDone:
		}
	}()

	// A write watchdog closes the upstream body if a flush blocks on a stalled
	// client beyond the window; closing the body unblocks the decode loop and
	// ends the handler, releasing the lease. The watchdog is armed around each
	// flush and disarmed after, so slow-but-progressing streams are not cut.
	var watchdog *WriteWatchdog
	if opts.WriteIdleTimeout > 0 {
		watchdog = NewWriteWatchdog(opts.WriteIdleTimeout, func() { _ = upstream.Body.Close() })
		defer watchdog.Stop()
	}

	decoder := NewDecoder(upstream.Body)
	encoder := NewEncoder(writer)

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	// Tell nginx-style reverse proxies not to buffer the stream — without it the
	// default `proxy_buffering on` batches SSE chunks until the buffer fills,
	// collapsing the streaming experience into one big client-side dump (audit
	// B6). Non-nginx proxies simply ignore the header.
	writer.Header().Set("X-Accel-Buffering", "no")

	seenDone := false
	firstDataWritten := false
	firstDataNotified := false
	var pending bytes.Buffer

	for {
		event, err := decoder.Decode()
		if err != nil {
			if err == io.EOF {
				if !seenDone {
					return ErrStreamInterrupted
				}
				return nil
			}
			// Body closed by context cancellation or network error
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("decode SSE event: %w", err)
		}
		if !firstDataWritten && len(event.Data) == 0 {
			var encoded bytes.Buffer
			if err := NewEncoder(&encoded).Encode(event); err != nil {
				return fmt.Errorf("encode pending SSE event: %w", err)
			}
			if encoded.Len() > MaxEventSize-pending.Len() {
				return ErrEventTooLarge
			}
			_, _ = pending.Write(encoded.Bytes())
			continue
		}

		isDone := false
		for i, data := range event.Data {
			if strings.TrimSpace(data) == "[DONE]" {
				isDone = true
				// A malformed upstream may pack data lines after the terminal
				// [DONE] inside the same event; truncate so they never reach the
				// client (checklist #53).
				event.Data = event.Data[:i+1]
				break
			}
		}

		if isDone {
			if seenDone {
				continue
			}
			seenDone = true
		}
		// Set the deadline before any response body write, including the first
		// WriteHeader and replayed comment/pending bytes. A ResponseController
		// deadline is the only cancellable boundary available to synchronous
		// ResponseWriter operations. When the writer does not support
		// deadlines (e.g. httptest, or a buffering reverse proxy) downgrade
		// to the watchdog alone instead of failing the stream.
		if err := SetWriteDeadline(writer, opts.WriteIdleTimeout); err != nil {
			if errors.Is(err, ErrWriteDeadlineUnsupported) {
				// Keep the watchdog: it captured its own timeout at construction
				// and is independent of opts, so it is exactly the "downgrade to
				// the watchdog alone" fallback described above. Stopping it here
				// left a stalled client with no bound at all and made the
				// ErrWriteDeadlineUnsupported branch below unreachable.
				opts.WriteIdleTimeout = 0
			} else {
				return err
			}
		}

		if !firstDataWritten {
			commitWriter := writer
			if opts.CommitState != nil {
				commitWriter = opts.CommitState.Wrap(writer)
			}
			commitWriter.WriteHeader(http.StatusOK)
			firstDataWritten = true
			if _, err := io.Copy(commitWriter, bytes.NewReader(pending.Bytes())); err != nil {
				return fmt.Errorf("write pending SSE events: %w", err)
			}
			pending.Reset()
		}
		if seenDone && opts.BeforeComplete != nil {
			if err := opts.BeforeComplete(); err != nil {
				return err
			}
		}

		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("encode SSE event: %w", err)
		}

		if watchdog != nil {
			watchdog.Arm()
		}
		if err := FlushWithDeadline(writer, flusher, opts.WriteIdleTimeout); err != nil {
			if watchdog != nil {
				watchdog.Disarm()
			}
			if errors.Is(err, ErrWriteDeadlineUnsupported) {
				// Writer does not support deadlines — fallback to plain
				// Flush. The watchdog still guards stalled clients.
				flusher.Flush()
			} else {
				return err
			}
		}
		if watchdog != nil {
			watchdog.Disarm()
			if watchdog.Fired() {
				return ErrStreamWriteStalled
			}
		}

		// The first token has reached the client once the first data event is
		// flushed; notify exactly once so TTFT sampling ignores trailing events.
		if !firstDataNotified && firstDataWritten {
			firstDataNotified = true
			if opts.OnFirstData != nil {
				opts.OnFirstData()
			}
		}

		// Expose each flushed data payload to the caller. The hook is optional
		// and runs after the flush so it never delays the stream. The terminal
		// [DONE] marker is delivered through OnComplete, not here.
		if opts.OnData != nil && !seenDone && len(event.Data) > 0 {
			opts.OnData(JoinData(event.Data))
		}

		if seenDone {
			if opts.OnComplete != nil {
				opts.OnComplete()
			}
			return nil
		}
	}
}

func SetWriteDeadline(writer http.ResponseWriter, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	if err := http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("%w: %v", ErrWriteDeadlineUnsupported, err)
	}
	return nil
}

func FlushWithDeadline(writer http.ResponseWriter, flusher http.Flusher, timeout time.Duration) error {
	if timeout <= 0 {
		flusher.Flush()
		return nil
	}
	if err := SetWriteDeadline(writer, timeout); err != nil {
		return err
	}
	// ResponseController documents that compliant writers make body writes
	// return after the deadline. Keep Flush synchronous so there is no
	// unbounded goroutine when a custom writer violates that contract.
	started := time.Now()
	flusher.Flush()
	_ = http.NewResponseController(writer).SetWriteDeadline(time.Time{})
	if time.Since(started) >= timeout {
		return ErrStreamWriteStalled
	}
	return nil
}

// WriteWatchdog detects a flush that stays blocked beyond the idle window. It
// fires once and runs the onStall callback (which closes the upstream body so
// the decode loop unblocks), then stays fired so the caller can distinguish a
// stalled write from a healthy one.
type WriteWatchdog struct {
	timeout  time.Duration
	onStall  func()
	newTimer watchdogTimerFactory

	mu    sync.Mutex
	timer watchdogTimer
	fired bool
}

type watchdogTimer interface {
	Stop() bool
}

type watchdogTimerFactory func(time.Duration, func()) watchdogTimer

func NewWriteWatchdog(timeout time.Duration, onStall func()) *WriteWatchdog {
	return newWriteWatchdogWithTimer(timeout, onStall, func(delay time.Duration, callback func()) watchdogTimer {
		return time.AfterFunc(delay, callback)
	})
}

func newWriteWatchdogWithTimer(timeout time.Duration, onStall func(), newTimer watchdogTimerFactory) *WriteWatchdog {
	return &WriteWatchdog{timeout: timeout, onStall: onStall, newTimer: newTimer}
}

// Arm arms the watchdog for one flush. It is a no-op after a prior stall: the
// stream is already being torn down.
func (w *WriteWatchdog) Arm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fired {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = w.newTimer(w.timeout, func() {
		w.mu.Lock()
		if w.fired {
			w.mu.Unlock()
			return
		}
		w.fired = true
		w.mu.Unlock()
		w.onStall()
	})
}

// Disarm cancels a pending stall. It is safe to call after the watchdog fired.
func (w *WriteWatchdog) Disarm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

func (w *WriteWatchdog) Fired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}

func (w *WriteWatchdog) Stop() {
	w.Disarm()
}
