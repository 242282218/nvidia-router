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
	defaultQueueCapacity = 100
	defaultQueueWait     = 60 * time.Second
	queueRetryAfter      = time.Second
)

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
	ctx       context.Context
	modelID   int64
	attempted map[int64]struct{}
	result    chan acquireResult
	element   *list.Element
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

func (p *Pool) Acquire(ctx context.Context, modelID int64, attempted map[int64]struct{}) (Lease, error) {
	return p.acquire(ctx, modelID, attempted, p.queueSettings())
}

func (p *Pool) AcquireWithSnapshot(
	ctx context.Context,
	modelID int64,
	attempted map[int64]struct{},
	snapshot runtimeconfig.Snapshot,
) (Lease, error) {
	return p.acquire(ctx, modelID, attempted, resolveQueueSettings(snapshot))
}

func (p *Pool) acquire(
	ctx context.Context,
	modelID int64,
	attempted map[int64]struct{},
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
		lease, unavailable := p.tryAcquireLocked(modelID, attempted)
		if lease != nil {
			p.mu.Unlock()
			return lease, nil
		}
		if unavailable.reason != UnavailableBusy {
			p.mu.Unlock()
			return nil, unavailableError(unavailable)
		}
	}
	if p.waiters.Len() >= settings.capacity {
		p.mu.Unlock()
		return nil, queueFullError()
	}

	waiter := &waiter{
		ctx:       ctx,
		modelID:   modelID,
		attempted: cloneAttempted(attempted),
		result:    make(chan acquireResult, 1),
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
	capacity int
	wait     time.Duration
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
	return resolvedQueueSettings{capacity: capacity, wait: wait}
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
		return nil, queueTimeoutError()
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
	remaining := p.waiters.Len()
	for remaining > 0 && p.waiters.Len() > 0 {
		waiter := p.waiters.front()
		if err := waiter.ctx.Err(); err != nil {
			p.waiters.remove(waiter)
			waiter.result <- acquireResult{err: err}
			remaining--
			continue
		}
		lease, unavailable := p.tryAcquireLocked(waiter.modelID, waiter.attempted)
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

func queueFullError() error {
	return &apierror.Error{
		Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "queue_full",
		Message: "The request queue is full.", RetryAfter: queueRetryAfter,
	}
}

func queueTimeoutError() error {
	return &apierror.Error{
		Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "queue_timeout",
		Message: "The request timed out while waiting for an upstream credential.", RetryAfter: queueRetryAfter,
	}
}

func shuttingDownError() error {
	return &apierror.Error{
		Status: http.StatusServiceUnavailable, Type: "server_error", Code: "server_shutting_down",
		Message: "The server is shutting down.",
	}
}
