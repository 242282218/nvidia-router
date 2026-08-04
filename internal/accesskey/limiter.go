package accesskey

import (
	"sync"
	"time"
)

var ErrRateLimited = &rateLimitError{}

type rateLimitError struct{}

func (*rateLimitError) Error() string { return "access key rate limit exceeded" }

type limiter struct {
	mu      sync.Mutex
	buckets map[int64]*limitBucket
}

type limitBucket struct {
	windowStart time.Time
	rpmCount    int
	tokens      int64
	concurrent  int
}

func newLimiter() *limiter { return &limiter{buckets: make(map[int64]*limitBucket)} }

func (l *limiter) begin(id int64, rpm, tpm, maxConcurrent int, now time.Time) error {
	if l == nil || (rpm <= 0 && tpm <= 0 && maxConcurrent <= 0) {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.bucketLocked(id, now)
	if maxConcurrent > 0 && bucket.concurrent >= maxConcurrent {
		return ErrRateLimited
	}
	if rpm > 0 && bucket.rpmCount >= rpm {
		return ErrRateLimited
	}
	if tpm > 0 && bucket.tokens >= int64(tpm) {
		return ErrRateLimited
	}
	bucket.rpmCount++
	bucket.concurrent++
	return nil
}

func (l *limiter) charge(id int64, tpm int, prompt, completion int64, now time.Time) {
	if l == nil || tpm <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.bucketLocked(id, now)
	bucket.tokens += maxInt64(prompt, 0) + maxInt64(completion, 0)
}

func (l *limiter) release(id int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if bucket := l.buckets[id]; bucket != nil && bucket.concurrent > 0 {
		bucket.concurrent--
	}
}

func (l *limiter) bucketLocked(id int64, now time.Time) *limitBucket {
	bucket := l.buckets[id]
	if bucket == nil {
		bucket = &limitBucket{windowStart: now}
		l.buckets[id] = bucket
		return bucket
	}
	if now.Before(bucket.windowStart) || now.Sub(bucket.windowStart) >= time.Minute {
		bucket.windowStart = now
		bucket.rpmCount = 0
		bucket.tokens = 0
		// Keep in-flight requests across fixed-window rotation; they still hold
		// a concurrency slot until their original request finishes.
	}
	return bucket
}

func maxInt64(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}
