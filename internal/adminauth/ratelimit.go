package adminauth

import (
	"container/list"
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
	clock           clock.Clock
	mu              sync.Mutex
	states          map[string]*loginState
	expirationOrder *list.List
}

type loginState struct {
	ip              string
	attempts        []time.Time
	failures        int
	lastUsed        time.Time
	expirationEntry *list.Element
}

func NewLoginLimiter(source clock.Clock) *LoginLimiter {
	if source == nil {
		source = clock.RealClock{}
	}
	return &LoginLimiter{
		clock:           source,
		states:          make(map[string]*loginState),
		expirationOrder: list.New(),
	}
}

func (l *LoginLimiter) StartAttempt(ip string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.cleanInactive(now)
	state := l.state(ip, now)
	state.attempts = keepWindowAttempts(state.attempts, now)
	l.touch(state, now)
	if len(state.attempts) >= loginAttemptLimit {
		return ErrLoginRateLimited
	}
	state.attempts = append(state.attempts, now)
	return nil
}

func (l *LoginLimiter) RecordFailure(ctx context.Context, ip string) error {
	l.mu.Lock()
	now := l.clock.Now()
	l.cleanInactive(now)
	state := l.state(ip, now)
	state.failures++
	l.touch(state, now)
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
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.cleanInactive(now)
	if state := l.states[ip]; state != nil {
		l.remove(state)
	}
}

func (l *LoginLimiter) state(ip string, now time.Time) *loginState {
	state := l.states[ip]
	if state == nil {
		state = &loginState{ip: ip, lastUsed: now}
		state.expirationEntry = l.expirationOrder.PushBack(state)
		l.states[ip] = state
	}
	return state
}

func (l *LoginLimiter) cleanInactive(now time.Time) {
	for {
		oldest := l.expirationOrder.Front()
		if oldest == nil {
			return
		}
		state := oldest.Value.(*loginState)
		if now.Sub(state.lastUsed) < loginStateLifetime {
			return
		}
		l.remove(state)
	}
}

func (l *LoginLimiter) touch(state *loginState, now time.Time) {
	state.lastUsed = now
	l.expirationOrder.MoveToBack(state.expirationEntry)
}

func (l *LoginLimiter) remove(state *loginState) {
	delete(l.states, state.ip)
	l.expirationOrder.Remove(state.expirationEntry)
	state.expirationEntry = nil
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
