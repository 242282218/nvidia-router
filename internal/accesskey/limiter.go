package accesskey

import (
	"sync"
	"time"
)

var (
	ErrRateLimited    = &rateLimitError{}
	ErrBudgetExceeded = &budgetError{}
)

type rateLimitError struct{}

func (*rateLimitError) Error() string { return "access key rate limit exceeded" }

// budgetError is returned when a key has consumed its cumulative token budget.
// It is distinct from ErrRateLimited so the middleware can emit a dedicated
// error code that an operator can alert on.
type budgetError struct{}

func (*budgetError) Error() string { return "access key token budget exceeded" }

type limiter struct {
	mu      sync.Mutex
	buckets map[int64]*limitBucket
}

type limitBucket struct {
	windowStart time.Time
	rpmCount    int
	tokens      int64
	concurrent  int
	consumed    int64 // cumulative tokens charged against the budget
	seeded      bool
}

func newLimiter() *limiter { return &limiter{buckets: make(map[int64]*limitBucket)} }

func (l *limiter) begin(id int64, rpm, tpm, maxConcurrent int, budget, persisted int64, now time.Time) error {
	if l == nil || (rpm <= 0 && tpm <= 0 && maxConcurrent <= 0 && budget <= 0) {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.bucketLocked(id, now)
	if !bucket.seeded {
		// First touch in this process seeds the budget from the last persisted
		// value so a restart does not reset a half-spent budget to zero.
		bucket.consumed = maxInt64(persisted, 0)
		bucket.seeded = true
	}
	if maxConcurrent > 0 && bucket.concurrent >= maxConcurrent {
		return ErrRateLimited
	}
	if rpm > 0 && bucket.rpmCount >= rpm {
		return ErrRateLimited
	}
	if tpm > 0 && bucket.tokens >= int64(tpm) {
		return ErrRateLimited
	}
	if budget > 0 && bucket.consumed >= budget {
		// A budget-exhausted key must reject before taking a slot so it does
		// not burn concurrent/rpm while failing.
		return ErrBudgetExceeded
	}
	bucket.rpmCount++
	bucket.concurrent++
	return nil
}

func (l *limiter) charge(id int64, tpm int, prompt, completion int64, now time.Time) {
	if l == nil {
		return
	}
	added := maxInt64(prompt, 0) + maxInt64(completion, 0)
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.bucketLocked(id, now)
	if tpm > 0 {
		bucket.tokens += added
	}
	// consumed accumulates regardless of tpm so a budget-only key (tpm=0) still
	// meters its lifetime token spend.
	bucket.consumed += added
}

// consumedTotal returns the cumulative charged tokens for a key, used by the
// service to persist budget progress back to the database.
func (l *limiter) consumedTotal(id int64) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if bucket := l.buckets[id]; bucket != nil {
		return bucket.consumed
	}
	return 0
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

// remove drops a key's bucket entirely. Called on revocation so a deleted key's
// rate-limit state does not accumulate in the map for the process lifetime.
func (l *limiter) remove(id int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.buckets, id)
	l.mu.Unlock()
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
		// Keep in-flight requests and the cumulative budget across the fixed
		// window rotation: they still hold a concurrency slot / consumed tokens
		// until their original request finishes.
	}
	return bucket
}

func maxInt64(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}
