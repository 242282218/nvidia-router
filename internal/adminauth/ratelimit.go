package adminauth

import (
	"context"
	"errors"
	"sync"
	"time"

	"nvidia-router/internal/clock"
)

const (
	loginWindow        = time.Minute
	loginAttemptLimit  = 5
	loginStateLifetime = 24 * time.Hour
)

var ErrLoginRateLimited = errors.New("login rate limited")

type LoginLimiter struct {
	clock  clock.Clock
	mu     sync.Mutex
	states map[string]*loginState
}

type loginState struct {
	attempts []time.Time
	failures int
	lastUsed time.Time
}

func NewLoginLimiter(source clock.Clock) *LoginLimiter {
	if source == nil {
		source = clock.RealClock{}
	}
	return &LoginLimiter{clock: source, states: make(map[string]*loginState)}
}

func (l *LoginLimiter) StartAttempt(ip string) error {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanInactive(now)
	state := l.state(ip, now)
	state.attempts = keepWindowAttempts(state.attempts, now)
	state.attempts = append(state.attempts, now)
	state.lastUsed = now
	if len(state.attempts) > loginAttemptLimit {
		return ErrLoginRateLimited
	}
	return nil
}

func (l *LoginLimiter) RecordFailure(ctx context.Context, ip string) error {
	now := l.clock.Now()
	l.mu.Lock()
	l.cleanInactive(now)
	state := l.state(ip, now)
	state.failures++
	state.lastUsed = now
	delay := loginFailureDelay(state.failures)
	l.mu.Unlock()

	timer := l.clock.NewTimer(delay)
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (l *LoginLimiter) RecordSuccess(ip string) {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanInactive(now)
	delete(l.states, ip)
}

func (l *LoginLimiter) state(ip string, now time.Time) *loginState {
	state := l.states[ip]
	if state == nil {
		state = &loginState{lastUsed: now}
		l.states[ip] = state
	}
	return state
}

func (l *LoginLimiter) cleanInactive(now time.Time) {
	for ip, state := range l.states {
		if now.Sub(state.lastUsed) > loginStateLifetime {
			delete(l.states, ip)
		}
	}
}

func (l *LoginLimiter) stateCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.states)
}

func keepWindowAttempts(attempts []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-loginWindow)
	kept := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	return kept
}

func loginFailureDelay(failures int) time.Duration {
	shift := min(failures-1, 3)
	return time.Second << shift
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
