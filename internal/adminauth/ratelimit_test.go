package adminauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/clock"
)

func TestLoginLimiterLimitsSixthAttemptInSlidingWindow(t *testing.T) {
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	limiter := NewLoginLimiter(testClock)
	for range 5 {
		if err := limiter.StartAttempt("192.0.2.1"); err != nil {
			t.Fatalf("StartAttempt before limit: %v", err)
		}
	}
	if err := limiter.StartAttempt("192.0.2.1"); !errors.Is(err, ErrLoginRateLimited) {
		t.Fatalf("sixth StartAttempt error = %v, want ErrLoginRateLimited", err)
	}
	testClock.Advance(61 * time.Second)
	if err := limiter.StartAttempt("192.0.2.1"); err != nil {
		t.Fatalf("StartAttempt after window: %v", err)
	}
}

func TestLoginLimiterBacksOffFailuresAndResetsAfterSuccess(t *testing.T) {
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	limiter := NewLoginLimiter(testClock)
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second} {
		if err := limiter.RecordFailure(context.Background(), "192.0.2.2"); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
		if got := testClock.LastTimerDuration(); got != want {
			t.Fatalf("failure delay = %s, want %s", got, want)
		}
	}
	limiter.RecordSuccess("192.0.2.2")
	if err := limiter.RecordFailure(context.Background(), "192.0.2.2"); err != nil {
		t.Fatalf("RecordFailure after success: %v", err)
	}
	if got := testClock.LastTimerDuration(); got != time.Second {
		t.Fatalf("failure delay after success = %s, want 1s", got)
	}
}

func TestLoginLimiterCleansInactiveState(t *testing.T) {
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	limiter := NewLoginLimiter(testClock)
	if err := limiter.StartAttempt("192.0.2.3"); err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	testClock.Advance(24*time.Hour + time.Nanosecond)
	if err := limiter.StartAttempt("192.0.2.4"); err != nil {
		t.Fatalf("StartAttempt cleanup trigger: %v", err)
	}
	if got := limiter.stateCount(); got != 1 {
		t.Fatalf("retained limiter states = %d, want 1", got)
	}
}

func TestLoginLimiterFailureCanBeCancelled(t *testing.T) {
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	testClock.blockTimers()
	limiter := NewLoginLimiter(testClock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.RecordFailure(ctx, "192.0.2.5"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RecordFailure cancellation error = %v, want context.Canceled", err)
	}
}

type limiterClock struct {
	clock.RealClock
	mu        sync.Mutex
	now       time.Time
	durations []time.Duration
	block     bool
}

func newLimiterClock(now time.Time) *limiterClock { return &limiterClock{now: now} }

func (c *limiterClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *limiterClock) NewTimer(duration time.Duration) *time.Timer {
	c.mu.Lock()
	c.durations = append(c.durations, duration)
	block := c.block
	c.mu.Unlock()
	if block {
		return time.NewTimer(time.Hour)
	}
	return time.NewTimer(0)
}

func (c *limiterClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func (c *limiterClock) LastTimerDuration() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.durations[len(c.durations)-1]
}

func (c *limiterClock) blockTimers() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.block = true
}
