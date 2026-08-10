package nvidia

import (
	"errors"
	"io"
	"sync"
	"time"
)

var ErrAudioStreamIdle = errors.New("upstream audio stream idle for too long")

func WithAudioIdleTimeout(body io.ReadCloser, idle time.Duration) io.ReadCloser {
	if body == nil || idle <= 0 {
		return body
	}
	wrapped := &audioIdleReadCloser{ReadCloser: body, idle: idle}
	wrapped.timer = time.AfterFunc(idle, wrapped.expire)
	return wrapped
}

type audioIdleReadCloser struct {
	io.ReadCloser
	idle    time.Duration
	timer   *time.Timer
	mu      sync.Mutex
	expired bool
	closed  bool
}

func (r *audioIdleReadCloser) expire() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.expired = true
	r.mu.Unlock()
	_ = r.ReadCloser.Close()
}

func (r *audioIdleReadCloser) Read(payload []byte) (int, error) {
	read, err := r.ReadCloser.Read(payload)
	if read > 0 {
		// Reset under the lock so it stays ordered with expire():
		// once expire() has flipped expired to true the timer is
		// already firing and Resetting it would resurrect it racing
		// the in-flight body.Close, double-closing the body.
		r.mu.Lock()
		if !r.expired && !r.closed {
			r.expired = false
			r.timer.Reset(r.idle)
		}
		r.mu.Unlock()
		return read, err
	}
	r.mu.Lock()
	expired := r.expired
	r.mu.Unlock()
	if expired {
		return read, ErrAudioStreamIdle
	}
	return read, err
}

func (r *audioIdleReadCloser) Close() error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.timer.Stop()
	}
	r.mu.Unlock()
	return r.ReadCloser.Close()
}
