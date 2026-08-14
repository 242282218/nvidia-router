package pool

import (
	"container/list"
	"context"
	"net/http"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/runtimeconfig"
)

const (
	defaultQueueCapacity      = 100
	defaultQueueWait          = 60 * time.Second
	defaultMaxStreamingPerKey = 2
	queueRetryAfter           = time.Second
)

type noAttemptedRelaxationKey struct{}

// WithNoAttemptedRelaxation marks a failover acquire so a queued retry cannot
// reuse a key that this request already attempted. Ordinary callers retain the
// queue's relaxed recovery behaviour for an attempted key whose lease was busy.
func WithNoAttemptedRelaxation(ctx context.Context) context.Context {
	return context.WithValue(ctx, noAttemptedRelaxationKey{}, true)
}

func noAttemptedRelaxation(ctx context.Context) bool {
	value, _ := ctx.Value(noAttemptedRelaxationKey{}).(bool)
	return value
}

type UnavailableReason uint8

const (
	UnavailableBusy UnavailableReason = iota
	UnavailableCooling
	UnavailableDisabled
	UnavailableModelBlocked
)

type unavailableState struct {
	reason     UnavailableReason
	retryAfter time.Duration
}

type acquireResult struct {
	lease Lease
	err   error
}

type waiter struct {
	ctx                context.Context
	modelID            int64
	attempted          map[int64]struct{}
	stream             bool
	maxStreamingPerKey int
	latencyEnabled     bool
	result             chan acquireResult
	element            *list.Element
}

type waitQueue struct {
	items list.List
}

func (q *waitQueue) Len() int {
	return q.items.Len()
}

func (q *waitQueue) push(waiter *waiter) {
	waiter.element = q.items.PushBack(waiter)
}

func (q *waitQueue) front() *waiter {
	front := q.items.Front()
	if front == nil {
		return nil
	}
	return front.Value.(*waiter)
}

func (q *waitQueue) remove(waiter *waiter) bool {
	if waiter.element == nil {
		return false
	}
	q.items.Remove(waiter.element)
	waiter.element = nil
	return true
}

func (p *Pool) Acquire(ctx context.Context, modelID int64, attempted map[int64]struct{}, stream bool) (Lease, error) {
	return p.acquire(ctx, modelID, attempted, stream, p.queueSettings())
}

func (p *Pool) AcquireWithSnapshot(
	ctx context.Context,
	modelID int64,
	attempted map[int64]struct{},
	snapshot runtimeconfig.Snapshot,
	stream bool,
) (Lease, error) {
	return p.acquire(ctx, modelID, attempted, stream, resolveQueueSettings(snapshot))
}

func (p *Pool) acquire(
	ctx context.Context,
	modelID int64,
	attempted map[int64]struct{},
	stream bool,
	settings resolvedQueueSettings,
) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, shuttingDownError()
	}
	p.dispatchWaitersLocked()
	if p.waiters.Len() == 0 {
		lease, unavailable := p.tryAcquireLocked(modelID, attempted, stream, settings.maxStreamingPerKey, settings.latencyEnabled, false)
		if lease != nil {
			p.mu.Unlock()
			return lease, nil
		}
		if unavailable.reason != UnavailableBusy {
			p.mu.Unlock()
			return nil, unavailableError(unavailable)
		}
		// A fresh retry attempt must not enqueue and then relax its attempted
		// set: that would immediately reacquire the same key after a proxy
		// failure, turning a one-key request into an unbounded retry loop.
		// Keep queueing when an unattempted candidate exists (it may simply be
		// busy), but fail fast once every eligible key is already attempted.
		if !p.hasUnattemptedEligibleLocked(modelID, attempted) &&
			!p.hasBusyAttemptedEligibleLocked(modelID, attempted, stream, settings.maxStreamingPerKey) {
			p.mu.Unlock()
			return nil, unavailableError(unavailableState{reason: UnavailableDisabled})
		}
	}
	if p.waiters.Len() >= settings.capacity {
		p.mu.Unlock()
		return nil, queueFullError(settings.wait)
	}

	waiter := &waiter{
		ctx:                ctx,
		modelID:            modelID,
		attempted:          cloneAttempted(attempted),
		stream:             stream,
		maxStreamingPerKey: settings.maxStreamingPerKey,
		latencyEnabled:     settings.latencyEnabled,
		result:             make(chan acquireResult, 1),
	}
	p.waiters.push(waiter)
	p.dispatchWaitersLocked()
	p.mu.Unlock()
	return p.waitForResult(ctx, waiter, settings.wait)
}

func (p *Pool) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for p.waiters.Len() > 0 {
		waiter := p.waiters.front()
		p.waiters.remove(waiter)
		waiter.result <- acquireResult{err: shuttingDownError()}
	}
}

type resolvedQueueSettings struct {
	capacity           int
	wait               time.Duration
	maxStreamingPerKey int
	latencyEnabled     bool
}

func (p *Pool) queueSettings() resolvedQueueSettings {
	snapshot := runtimeconfig.Snapshot{}
	if p.settings != nil {
		snapshot = p.settings.Snapshot()
	}
	return resolveQueueSettings(snapshot)
}

func resolveQueueSettings(snapshot runtimeconfig.Snapshot) resolvedQueueSettings {
	capacity := snapshot.QueueCapacity
	if capacity <= 0 {
		capacity = defaultQueueCapacity
	}
	wait := time.Duration(snapshot.QueueWaitTimeoutMS) * time.Millisecond
	if wait <= 0 {
		wait = defaultQueueWait
	}
	maxStreaming := snapshot.MaxStreamingPerKey
	if maxStreaming <= 0 {
		maxStreaming = defaultMaxStreamingPerKey
	}
	return resolvedQueueSettings{capacity: capacity, wait: wait, maxStreamingPerKey: maxStreaming, latencyEnabled: snapshot.LatencyRoutingEnabled}
}

func (p *Pool) waitForResult(ctx context.Context, waiter *waiter, wait time.Duration) (Lease, error) {
	timer := p.clock.NewTimer(wait)
	defer timer.Stop()
	select {
	case result := <-waiter.result:
		return result.lease, result.err
	case <-ctx.Done():
		p.abandonWaiter(waiter)
		return nil, ctx.Err()
	case <-timer.C:
		p.abandonWaiter(waiter)
		return nil, queueTimeoutError(wait)
	}
}

func (p *Pool) abandonWaiter(waiter *waiter) {
	p.mu.Lock()
	if p.waiters.remove(waiter) {
		p.dispatchWaitersLocked()
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	result := <-waiter.result
	if result.lease != nil {
		result.lease.Release()
	}
}

func (p *Pool) dispatchWaitersLocked() {
	if p.closed {
		return
	}
	// First pass: normal acquire (attempted keys stay excluded). A busy head
	// waiter rotates to the tail so a later waiter that can use a different key
	// is not stalled behind it.
	remaining := p.waiters.Len()
	for remaining > 0 && p.waiters.Len() > 0 {
		waiter := p.waiters.front()
		if err := waiter.ctx.Err(); err != nil {
			p.waiters.remove(waiter)
			waiter.result <- acquireResult{err: err}
			remaining--
			continue
		}
		lease, unavailable := p.tryAcquireLocked(waiter.modelID, waiter.attempted, waiter.stream, waiter.maxStreamingPerKey, waiter.latencyEnabled, false)
		if lease == nil && unavailable.reason == UnavailableBusy {
			// A busy head waiter must not stall the queue: rotate it to the
			// tail and keep scanning. remaining bounds the pass so a queue of
			// busy-only waiters cannot loop forever.
			p.waiters.remove(waiter)
			p.waiters.push(waiter)
			remaining--
			continue
		}
		p.waiters.remove(waiter)
		remaining--
		if lease != nil {
			waiter.result <- acquireResult{lease: lease}
			continue
		}
		waiter.result <- acquireResult{err: unavailableError(unavailable)}
	}

	// Every waiter was busy-stalled: nothing could be served in the pass above.
	// If a waiter's attempted set excludes the only key that is now idle, it
	// would rotate forever while the key sits unused (single-key pool after a
	// failover). Give the head waiter one relaxed pass that retries idle
	// attempted keys — but only when no other ready key exists that a later
	// dispatch could serve. If the pool still has a non-attempted ready key
	// (even one that is momentarily busy), a later waiter or dispatch can take
	// it, so the attempted key should keep waiting rather than burn a retry on
	// the very key it just failed.
	if p.waiters.Len() > 0 && remaining == 0 {
		waiter := p.waiters.front()
		if !noAttemptedRelaxation(waiter.ctx) &&
			!p.hasNonAttemptedReadyLocked(waiter.modelID, waiter.attempted, waiter.stream, waiter.maxStreamingPerKey, waiter.latencyEnabled) {
			lease, unavailable := p.tryAcquireLocked(waiter.modelID, waiter.attempted, waiter.stream, waiter.maxStreamingPerKey, waiter.latencyEnabled, true)
			if lease != nil {
				p.waiters.remove(waiter)
				waiter.result <- acquireResult{lease: lease}
			} else if unavailable.reason != UnavailableBusy {
				p.waiters.remove(waiter)
				waiter.result <- acquireResult{err: unavailableError(unavailable)}
			}
		}
	}
}

// hasNonAttemptedReadyLocked reports whether the pool holds a ready key outside
// the waiter's attempted set that could serve the request RIGHT NOW. Used to
// decide whether relaxing the attempted exclusion is safe: a key that is busy
// (or streaming-saturated) is not assignable, and counting it as "ready" left
// the head waiter stalled while the only idle key sat in its attempted set
// (single-head stall: W1 attempted A, A idle, B busy and un-attempted — the
// relaxed pass never fired and W1 waited on B's release or the queue timeout).
func (p *Pool) hasNonAttemptedReadyLocked(modelID int64, attempted map[int64]struct{}, stream bool, maxStreamingPerKey int, latencyEnabled bool) bool {
	now := p.clock.Now()
	for offset := range p.order {
		index := (p.cursor + offset) % len(p.order)
		state := p.keys[p.order[index]]
		if !state.snapshot.Enabled || state.snapshot.AuthInvalid {
			continue
		}
		if _, blocked := state.blocks[modelID]; blocked {
			continue
		}
		if state.snapshot.CooldownUntil != nil && now.Before(*state.snapshot.CooldownUntil) {
			continue
		}
		if _, alreadyAttempted := attempted[state.snapshot.ID]; alreadyAttempted {
			continue
		}
		if stream {
			if state.streamingBusy >= maxStreamingPerKey {
				continue
			}
		} else {
			if state.busy {
				continue
			}
		}
		return true
	}
	return false
}

// hasUnattemptedEligibleLocked reports whether any enabled, unblocked,
// non-cooling key remains outside attempted. Busy keys count: a fresh acquire
// should wait for one of them, while a request whose entire eligible set was
// already attempted must stop instead of entering the relaxed waiter path.
func (p *Pool) hasUnattemptedEligibleLocked(modelID int64, attempted map[int64]struct{}) bool {
	now := p.clock.Now()
	for _, id := range p.order {
		state := p.keys[id]
		if !state.snapshot.Enabled || state.snapshot.AuthInvalid {
			continue
		}
		if _, blocked := state.blocks[modelID]; blocked {
			continue
		}
		if state.snapshot.CooldownUntil != nil && now.Before(*state.snapshot.CooldownUntil) {
			continue
		}
		if _, alreadyAttempted := attempted[state.snapshot.ID]; alreadyAttempted {
			continue
		}
		return true
	}
	return false
}

// hasBusyAttemptedEligibleLocked reports whether waiting can still make
// progress by reusing an attempted key after its current lease is released.
// This preserves the queued-waiter recovery path while preventing an idle
// attempted key from being reacquired immediately by a fresh retry.
func (p *Pool) hasBusyAttemptedEligibleLocked(modelID int64, attempted map[int64]struct{}, stream bool, maxStreamingPerKey int) bool {
	now := p.clock.Now()
	for _, id := range p.order {
		state := p.keys[id]
		if !state.snapshot.Enabled || state.snapshot.AuthInvalid {
			continue
		}
		if _, blocked := state.blocks[modelID]; blocked {
			continue
		}
		if state.snapshot.CooldownUntil != nil && now.Before(*state.snapshot.CooldownUntil) {
			continue
		}
		if _, alreadyAttempted := attempted[state.snapshot.ID]; !alreadyAttempted {
			continue
		}
		if stream {
			if state.streamingBusy >= maxStreamingPerKey {
				return true
			}
		} else if state.busy {
			return true
		}
	}
	return false
}

func cloneAttempted(attempted map[int64]struct{}) map[int64]struct{} {
	if len(attempted) == 0 {
		return nil
	}
	cloned := make(map[int64]struct{}, len(attempted))
	for keyID := range attempted {
		cloned[keyID] = struct{}{}
	}
	return cloned
}

func unavailableError(unavailable unavailableState) error {
	switch unavailable.reason {
	case UnavailableCooling:
		return &apierror.Error{
			Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "all_keys_cooling_down",
			Message: "All available upstream credentials are cooling down.", RetryAfter: unavailable.retryAfter,
		}
	case UnavailableModelBlocked:
		return &apierror.Error{
			Status: http.StatusNotFound, Type: "invalid_request_error", Code: "model_not_available",
			Message: "The model is not available for any upstream credential.",
		}
	default:
		return &apierror.Error{
			Status: http.StatusServiceUnavailable, Type: "server_error", Code: "no_available_keys",
			Message: "No upstream credentials are currently available.",
		}
	}
}

func queueFullError(retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = queueRetryAfter
	}
	return &apierror.Error{
		Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "queue_full",
		Message: "The request queue is full.", RetryAfter: retryAfter,
	}
}

func queueTimeoutError(retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = queueRetryAfter
	}
	return &apierror.Error{
		Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "queue_timeout",
		Message: "The request timed out while waiting for an upstream credential.", RetryAfter: retryAfter,
	}
}

func shuttingDownError() error {
	return &apierror.Error{
		Status: http.StatusServiceUnavailable, Type: "server_error", Code: "server_shutting_down",
		Message: "The server is shutting down.",
	}
}
