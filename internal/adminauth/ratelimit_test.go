package adminauth

import (
	"container/list"
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/clock"
)

func TestLoginRateLimiterLimitsSixthAttemptInSlidingWindow(t *testing.T) {
	start := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	t.Run("inside window", func(t *testing.T) {
		testClock := newLimiterClock(start)
		limiter := NewLoginLimiter(testClock)
		fillLoginWindow(t, limiter, "192.0.2.1")
		testClock.Advance(loginWindow - time.Nanosecond)
		if err := limiter.StartAttempt("192.0.2.1"); !errors.Is(err, ErrLoginRateLimited) {
			t.Fatalf("StartAttempt inside window error = %v, want ErrLoginRateLimited", err)
		}
	})
	t.Run("exact boundary", func(t *testing.T) {
		testClock := newLimiterClock(start)
		limiter := NewLoginLimiter(testClock)
		fillLoginWindow(t, limiter, "192.0.2.1")
		testClock.Advance(loginWindow)
		if err := limiter.StartAttempt("192.0.2.1"); err != nil {
			t.Fatalf("StartAttempt at exact window boundary: %v", err)
		}
	})
}

func TestLoginRateLimiterKeepsRejectedAttemptStateBounded(t *testing.T) {
	limiter := NewLoginLimiter(newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)))
	fillLoginWindow(t, limiter, "192.0.2.2")
	for range 100 {
		if err := limiter.StartAttempt("192.0.2.2"); !errors.Is(err, ErrLoginRateLimited) {
			t.Fatalf("rejected StartAttempt error = %v, want ErrLoginRateLimited", err)
		}
	}
	if got := limiterAttemptCount(limiter, "192.0.2.2"); got != loginAttemptLimit {
		t.Fatalf("stored attempts after sustained rejection = %d, want %d", got, loginAttemptLimit)
	}
}

func TestLoginRateLimiterMaintainsOrderedExpirationIndex(t *testing.T) {
	limiter := NewLoginLimiter(newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)))
	field, found := reflect.TypeOf(*limiter).FieldByName("expirationOrder")
	if !found {
		t.Fatal("LoginLimiter has no ordered expiration index")
	}
	if want := reflect.TypeOf((*list.List)(nil)); field.Type != want {
		t.Fatalf("expiration index type = %v, want %v", field.Type, want)
	}
}

func TestLoginRateLimiterAllowsOnlyFiveConcurrentAttemptsPerIP(t *testing.T) {
	limiter := NewLoginLimiter(newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)))
	const callers = 20
	start := make(chan struct{})
	results := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- limiter.StartAttempt("192.0.2.3")
		}()
	}
	close(start)
	group.Wait()
	close(results)

	succeeded, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLoginRateLimited):
			limited++
		default:
			t.Fatalf("unexpected StartAttempt error: %v", err)
		}
	}
	if succeeded != loginAttemptLimit || limited != callers-loginAttemptLimit {
		t.Fatalf("concurrent attempts succeeded=%d limited=%d, want %d and %d", succeeded, limited, loginAttemptLimit, callers-loginAttemptLimit)
	}
}

func TestLoginRateLimiterTracksIPsIndependently(t *testing.T) {
	limiter := NewLoginLimiter(newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)))
	fillLoginWindow(t, limiter, "192.0.2.4")
	if err := limiter.StartAttempt("192.0.2.4"); !errors.Is(err, ErrLoginRateLimited) {
		t.Fatalf("limited IP error = %v, want ErrLoginRateLimited", err)
	}
	for range loginAttemptLimit {
		if err := limiter.StartAttempt("192.0.2.5"); err != nil {
			t.Fatalf("independent IP StartAttempt: %v", err)
		}
	}
}

func TestLoginRateLimiterBacksOffFailuresAndResetsAfterSuccess(t *testing.T) {
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

func TestLoginRateLimiterCleansInactiveState(t *testing.T) {
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	limiter := NewLoginLimiter(testClock)
	if err := limiter.StartAttempt("192.0.2.3"); err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	testClock.Advance(24 * time.Hour)
	if err := limiter.StartAttempt("192.0.2.4"); err != nil {
		t.Fatalf("StartAttempt cleanup trigger: %v", err)
	}
	if got := limiter.stateCount(); got != 1 {
		t.Fatalf("retained limiter states = %d, want 1", got)
	}
}

func TestLoginRateLimiterExpirationOrderFollowsLastUse(t *testing.T) {
	start := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	testClock := newLimiterClock(start)
	limiter := NewLoginLimiter(testClock)
	if err := limiter.StartAttempt("recently-touched"); err != nil {
		t.Fatalf("StartAttempt first IP: %v", err)
	}
	testClock.Advance(time.Hour)
	if err := limiter.StartAttempt("expired"); err != nil {
		t.Fatalf("StartAttempt second IP: %v", err)
	}
	testClock.Advance(22 * time.Hour)
	if err := limiter.StartAttempt("recently-touched"); err != nil {
		t.Fatalf("touch first IP: %v", err)
	}
	testClock.Advance(2 * time.Hour)
	if err := limiter.StartAttempt("cleanup-trigger"); err != nil {
		t.Fatalf("StartAttempt cleanup trigger: %v", err)
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.states["expired"] != nil {
		t.Fatal("expiration index retained the 24-hour-old state")
	}
	if limiter.states["recently-touched"] == nil {
		t.Fatal("expiration index removed a recently touched state")
	}
}

func fillLoginWindow(t *testing.T, limiter *LoginLimiter, ip string) {
	t.Helper()
	for range loginAttemptLimit {
		if err := limiter.StartAttempt(ip); err != nil {
			t.Fatalf("StartAttempt before limit: %v", err)
		}
	}
}

func limiterAttemptCount(limiter *LoginLimiter, ip string) int {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return len(limiter.states[ip].attempts)
}

func TestLoginRateLimiterFailureCanBeCancelled(t *testing.T) {
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

func BenchmarkLoginRateLimiterStartAttemptWithManyActiveIPs(b *testing.B) {
	for _, stateCount := range []int{1_000, 10_000} {
		b.Run(strconv.Itoa(stateCount), func(b *testing.B) {
			testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
			limiter := NewLoginLimiter(testClock)
			for index := range stateCount {
				if err := limiter.StartAttempt("benchmark-ip-" + strconv.Itoa(index)); err != nil {
					b.Fatalf("populate limiter: %v", err)
				}
			}
			b.ResetTimer()
			for range b.N {
				_ = limiter.StartAttempt("benchmark-active-ip")
			}
		})
	}
}
