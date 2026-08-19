package sse

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrStreamIdle reports that the upstream stopped sending bytes for the whole
// idle window, so the connection is presumed dead rather than merely slow.
// Callers treat it like any post-commit truncation: the client already has a
// committed response, so nothing more can be written.
var ErrStreamIdle = errors.New("upstream stream idle for too long")

// WithIdleTimeout wraps body so a stalled upstream cannot pin a lease forever.
// Any Read that returns data restarts the window; when the window expires the
// underlying body is closed, which unblocks the in-flight Read with an error.
// A non-positive idle returns body unchanged.
func WithIdleTimeout(body io.ReadCloser, idle time.Duration) io.ReadCloser {
	if idle <= 0 {
		return body
	}
	wrapper := &idleReadCloser{ReadCloser: body, idle: idle}
	wrapper.timer = time.AfterFunc(idle, func() {
		wrapper.mu.Lock()
		if wrapper.closed {
			wrapper.mu.Unlock()
			return
		}
		wrapper.expired = true
		wrapper.mu.Unlock()
		_ = body.Close()
	})
	return wrapper
}

type idleReadCloser struct {
	io.ReadCloser
	idle    time.Duration
	timer   *time.Timer
	mu      sync.Mutex
	expired bool
	closed  bool
}

// MarkComplete forwards semantic stream completion through the idle wrapper.
func (r *idleReadCloser) MarkComplete() {
	if marker, ok := r.ReadCloser.(interface{ MarkComplete() }); ok {
		marker.MarkComplete()
	}
}

// RequireSemanticCompletion forwards the stream contract through the idle wrapper.
func (r *idleReadCloser) RequireSemanticCompletion() {
	if marker, ok := r.ReadCloser.(interface{ RequireSemanticCompletion() }); ok {
		marker.RequireSemanticCompletion()
	}
}

func (r *idleReadCloser) Read(payload []byte) (int, error) {
	read, err := r.ReadCloser.Read(payload)
	if read > 0 {
		// Any byte is progress; keep-alive comments and deltas both
		// count. Only reset when the idle callback has not already
		// fired and the wrapper is not closed: once expired/closed, the
		// stream is being torn down and a late Reset would resurrect the
		// timer racing the callback's body.Close.
		r.mu.Lock()
		if !r.expired && !r.closed {
			// Reset on a fired AfterFunc timer is safe, but the timer may
			// have already executed its callback. The expired guard above
			// ensures we do not resurrect a dead timer.
			r.timer.Reset(r.idle)
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	expired := r.expired
	r.mu.Unlock()
	if err != nil && expired {
		return read, ErrStreamIdle
	}
	return read, err
}

func (r *idleReadCloser) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return r.ReadCloser.Close()
	}
	r.closed = true
	if !r.expired {
		r.timer.Stop()
	}
	r.expired = true
	r.mu.Unlock()
	return r.ReadCloser.Close()
}
