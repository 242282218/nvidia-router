package router

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

type ExecuteFunc func(
	ctx context.Context,
	keyID int64,
	secret []byte,
	commit *CommitState,
) (*http.Response, error)

type KeyPool interface {
	AcquireWithSnapshot(
		ctx context.Context,
		modelID int64,
		attempted map[int64]struct{},
		snapshot runtimeconfig.Snapshot,
		stream bool,
	) (pool.Lease, error)
}

type SecretProvider interface {
	WithSecret(ctx context.Context, keyID int64, callback func([]byte) error) error
}

type KeyStateWriter interface {
	MarkSuccess(ctx context.Context, keyID int64) (keystate.KeySnapshot, error)
	MarkFailure(ctx context.Context, keyID, modelID int64, f fault.Fault) (keystate.KeySnapshot, error)
}

type StateSync interface {
	ApplySuccess(keyID int64)
	ApplyFailure(keyID, modelID int64, f fault.Fault, persisted keystate.KeySnapshot)
}

// LatencyObserver receives per-attempt success durations from the router so
// the scheduler can prefer faster keys. The pool implements it; it is optional
// so tests can construct an Attempt without one.
type LatencyObserver interface {
	RecordLatency(keyID int64, durationMS int64)
}

type AttemptResult struct {
	Response *http.Response
	Lease    pool.Lease
	Commit   *CommitState
	Attempts int
	Context  context.Context
	cancel   context.CancelFunc
}

// Commit records that the upstream has produced a client-visible response.
// Streaming handlers call it after priming the first byte, before any later
// read error can be mistaken for a retryable pre-commit failure.
func (s *CommitState) Commit() {
	if s != nil {
		s.committed.Store(true)
	}
}

func (r AttemptResult) Release() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.Lease != nil {
		r.Lease.Release()
	}
}

type Attempt struct {
	settings  runtimeconfig.Provider
	keyPool   KeyPool
	secrets   SecretProvider
	states    KeyStateWriter
	stateSync StateSync
	clock     clock.Clock
	latency   LatencyObserver
}

func NewAttempt(
	settings runtimeconfig.Provider,
	keyPool KeyPool,
	secrets SecretProvider,
	states KeyStateWriter,
	stateSync StateSync,
	source clock.Clock,
	latencyObservers ...LatencyObserver,
) *Attempt {
	if source == nil {
		source = clock.RealClock{}
	}
	attempt := &Attempt{
		settings:  settings,
		keyPool:   keyPool,
		secrets:   secrets,
		states:    states,
		stateSync: stateSync,
		clock:     source,
	}
	if len(latencyObservers) > 0 {
		attempt.latency = latencyObservers[0]
	}
	return attempt
}

func (a *Attempt) Run(ctx context.Context, modelID int64, stream bool, execute ExecuteFunc) (AttemptResult, error) {
	now := a.clock.Now()
	settings := a.settings.Snapshot()
	// Apply per-model timeout overrides when the handler layer injected them.
	// The handler resolves the model before calling Run and knows which overrides
	// are configured; merging here keeps the Budget builder and all downstream
	// paths (primeSSE, WithIdleTimeout) consistent with each other.
	if hints, ok := runtimeconfig.ModelTimeoutsFromContext(ctx); ok {
		if hints.StreamFirstTokenTimeoutMS > 0 {
			settings.StreamFirstTokenTimeoutMS = hints.StreamFirstTokenTimeoutMS
		}
		if hints.StreamIdleTimeoutMS > 0 {
			settings.StreamIdleTimeoutMS = hints.StreamIdleTimeoutMS
		}
	}
	budget := newBudget(settings, now, stream)
	requestCtx, cancel := a.requestContext(ctx, budget)
	keepContext := false
	defer func() {
		if !keepContext {
			cancel()
		}
	}()
	// The budget rides on the request context so the acquire stage can honour the
	// retry window for streams (which carry no total deadline). The execute stage
	// re-attaches a per-attempt budget below, so this value never leaks into the
	// handler's budget reads.
	requestCtx = withBudget(requestCtx, budget)

	// Build the failover matcher once per request: the spec lives on the same
	// snapshot the rest of the budget reads, so decoding it during Run keeps the
	// retry policy consistent with the timeouts above (audit B4).
	failover := buildFailoverMatcher(settings.FailoverStatusCodes)

	attempted := make(map[int64]struct{})
	var totalQueue time.Duration
	var lastFault *fault.Fault
	for {
		queueStarted := a.clock.Now()
		lease, err := a.acquire(requestCtx, settings, modelID, attempted, stream)
		totalQueue += a.clock.Now().Sub(queueStarted)
		if err != nil {
			// A queue-limit answer (429) is the honest signal for "we were
			// waiting in line": prefer it over a stale upstream fault from an
			// earlier attempt. Otherwise a total-deadline expiry that coincides
			// with the queue would surface the wrong error class (an old 5xx
			// instead of queue_timeout), and clients would mis-retry.
			if isQueueLimitError(err) {
				return AttemptResult{}, err
			}
			if lastFault != nil && a.budgetExpired(requestCtx) {
				return AttemptResult{}, *lastFault
			}
			return AttemptResult{}, chooseAcquireError(err, lastFault)
		}
		attempted[lease.KeyID()] = struct{}{}
		observability.SetAttempt(ctx, lease.KeyID(), len(attempted), totalQueue)

		// The first-byte budget starts once a lease is acquired; queue time is
		// bounded separately by the pool's queue-wait setting (queue_timeout).
		attemptStarted := a.clock.Now()
		executeCtx := withBudget(requestCtx, budget.forAttempt(a.clock.Now()))
		response, commit, currentFault, err := a.executeLease(executeCtx, requestCtx, modelID, lease, execute)
		if err != nil {
			return AttemptResult{}, err
		}
		if currentFault == nil {
			// Feed the success latency back so the pool's latency-aware
			// scheduler can prefer keys that answer faster (and downgrade keys
			// that have quietly become slow). Best-effort: a nil observer is a
			// no-op and a busy pool never blocks the request path for it.
			if a.latency != nil {
				a.latency.RecordLatency(lease.KeyID(), a.clock.Now().Sub(attemptStarted).Milliseconds())
			}
			observability.SetUpstreamRequestID(ctx, response.Header.Get("X-Request-ID"))
			keepContext = true
			return AttemptResult{Response: response, Lease: lease, Commit: commit, Attempts: len(attempted), Context: requestCtx, cancel: cancel}, nil

		}
		lastFault = currentFault
		// Failover decision combines Classify's retryable flag with the
		// operator-tunable matcher (audit B4): a fault retries when the legacy
		// Retryable flag matches the gpt-load-era behaviour, OR when the
		// operator explicitly added the status code to the failover spec. The
		// union preserves pre-existing semantics for 401/403 (Retryable but not
		// in the default spec) while letting operators widen the set without a
		// release. A committed response and an expired budget remain hard
		// stoppers because they describe request-state, not retry policy.
		if !shouldRetry(currentFault, failover) || commit.Committed() || a.budgetExpired(requestCtx) {
			return AttemptResult{}, *currentFault
		}
		// Attempt cap and retry window are checked here rather than through the
		// request context: a stream's context must stay open for the body, so
		// bounding the loop via ctx would truncate committed streams.
		if len(attempted) >= budget.maxAttempts {
			return AttemptResult{}, *currentFault
		}
		if !budget.retryDeadline.IsZero() && !a.clock.Now().Before(budget.retryDeadline) {
			return AttemptResult{}, *currentFault
		}
		// Back off before the next key. Retrying immediately turned a struggling
		// upstream into a burst of N requests as fast as the pool could hand out
		// leases; the jitter keeps concurrent requests from re-synchronising.
		// An upstream Retry-After (typically on 429) overrides the exponential
		// delay so an account-level rate limit is not hammered again instantly.
		if err := a.backoff(requestCtx, len(attempted), budget, currentFault); err != nil {
			return AttemptResult{}, *currentFault
		}
	}
}

// shouldRetry decides whether an Attempt should acquire a different key and
// replay the request after currentFault. A configured failover matcher widens
// Classify's retryable flag (audit B4): the legacy Retryable stays the default
// behaviour for faults the operator did not opt into (401/403 default-retry
// on credential policy), and the matcher only ever adds status codes the
// operator explicitly whitelisted for failover.
func shouldRetry(currentFault *fault.Fault, matcher fault.FailoverMatcher) bool {
	if currentFault.Retryable {
		return true
	}
	if matcher.IsEmpty() {
		return false
	}
	return matcher.Match(currentFault.HTTPStatus)
}

// buildFailoverMapper turns the configured spec into a matcher. An empty spec is
// the legacy sentinel: the caller wants the documented default failover set so
// pre-configured operators see 429/5xx behaviour identical to the previous
// hardcode. A parse failure (only reachable from pathologically bad rows; the
// admin handler validates at store time) likewise yields the default rather
// than silently "never fail over", which would amplify a config typo into every
// upstream blip surfacing as a client-visible 5xx.
func buildFailoverMatcher(spec string) fault.FailoverMatcher {
	if strings.TrimSpace(spec) == "" {
		return fault.MustFailoverMatcher(fault.DefaultFailoverStatusCodes)
	}
	matcher, err := fault.NewFailoverMatcher(spec)
	if err != nil {
		return fault.MustFailoverMatcher(fault.DefaultFailoverStatusCodes)
	}
	return matcher
}

func (a *Attempt) requestContext(ctx context.Context, budget Budget) (context.Context, context.CancelFunc) {
	if budget.totalDeadline.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, budget.totalDeadline)
}

func (a *Attempt) acquire(
	ctx context.Context,
	settings runtimeconfig.Snapshot,
	modelID int64,
	attempted map[int64]struct{},
	stream bool,
) (pool.Lease, error) {
	if len(attempted) > 0 {
		ctx = pool.WithNoAttemptedRelaxation(ctx)
	}
	// A stream carries no total deadline on its request context, so without an
	// explicit bound here its queue wait would run for the whole pool queue-wait
	// window even when the operator sets queue_wait_timeout_ms above the retry
	// budget. Clamp the acquire context to the retry deadline: the queue is part
	// of the pre-commit phase, and the loop's own retryDeadline check is
	// unreachable while the request is parked in the queue.
	if stream {
		if budget, ok := BudgetFromContext(ctx); ok && !budget.retryDeadline.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, budget.retryDeadline)
			defer cancel()
		}
	}
	lease, err := a.keyPool.AcquireWithSnapshot(ctx, modelID, attempted, settings, stream)
	if errors.Is(err, context.DeadlineExceeded) {
		// During acquire the only thing we waited on is a free credential slot:
		// no upstream call happened yet, so a deadline here is a queue wait, not
		// an upstream timeout. Report it as retryable 429 queue_timeout instead of
		// 504 so clients back off and retry instead of treating the service as
		// unhealthy. This also keeps queue_timeout reachable when the operator
		// configures queue_wait_timeout_ms larger than the non-stream total
		// deadline (the pool timer would otherwise lose the race to ctx.Done).
		return nil, &apierror.Error{
			Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "queue_timeout",
			Message:    "The request timed out while waiting for an upstream credential.",
			RetryAfter: a.queueRetryAfter(settings),
		}
	}
	return lease, err
}

// queueRetryAfter returns the Retry-After for queue-limit answers, aligned with
// the operator's queue-wait setting so clients do not hammer the pool back on a
// fixed one-second cadence that is shorter than the queue horizon.
func (a *Attempt) queueRetryAfter(settings runtimeconfig.Snapshot) time.Duration {
	if wait := time.Duration(settings.QueueWaitTimeoutMS) * time.Millisecond; wait > 0 {
		return wait
	}
	return time.Second
}

// isQueueLimitError reports whether the acquire failed because the pool's own
// queue rejected the request (429 queue_timeout/queue_full). Such answers carry
// Retry-After and must win over a stale upstream fault when the total budget
// and the queue timer race.
func isQueueLimitError(err error) bool {
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusTooManyRequests &&
		(apiErr.Code == "queue_timeout" || apiErr.Code == "queue_full")
}

func (a *Attempt) executeLease(
	executeCtx context.Context,
	stateCtx context.Context,
	modelID int64,
	lease pool.Lease,
	execute ExecuteFunc,
) (response *http.Response, commit *CommitState, resultFault *fault.Fault, resultErr error) {
	keepLease := false
	defer func() {
		if !keepLease {
			lease.Release()
		}
	}()

	commit = &CommitState{}
	var executeErr error
	secretErr := a.secrets.WithSecret(executeCtx, lease.KeyID(), func(secret []byte) error {
		response, executeErr = execute(executeCtx, lease.KeyID(), secret, commit)
		return nil
	})
	if secretErr != nil {
		closeResponse(response)
		return nil, commit, nil, fmt.Errorf("use NVIDIA key secret: %w", secretErr)
	}
	if executeErr != nil {
		var proxyErr *xkproxy.Error
		if errors.As(executeErr, &proxyErr) {
			closeResponse(response)
			switch proxyErr.Reason() {
			case xkproxy.ReasonNoHealthyProxy:
				// A momentarily empty proxy pool is not a key fault: the request never
				// reached the upstream, so cooldowning the key would take every key
				// offline while the collector refills. Surface a retryable 503 instead
				// and let the retry loop switch keys with backoff, giving the collector
				// time to land the next fetch (audit D3).
				classified := &fault.Fault{
					HTTPStatus: http.StatusServiceUnavailable, Scope: fault.ScopeUpstreamGlobal, Retryable: true,
					PublicType: "server_error", PublicCode: "upstream_proxy_unavailable",
					PublicMessage: "The upstream proxy pool is temporarily unavailable.", Cause: proxyErr,
				}
				return nil, commit, classified, nil
			case xkproxy.ReasonTransportFailed:
				// A transport-level proxy failure is also not a key fault: the exit
				// died mid-dial, and the request likely never reached NVIDIA. Returning
				// the raw error here would short-circuit Run's retry loop before it can
				// switch keys (audit R5) — the very loop that would acquire a fresh
				// exit and replay. Classify it as a retryable upstream fault so the
				// next key attempt actually happens, matching ReasonNoHealthyProxy.
				classified := &fault.Fault{
					HTTPStatus: http.StatusBadGateway, Scope: fault.ScopeUpstreamGlobal, Retryable: true,
					PublicType: "server_error", PublicCode: "upstream_proxy_unavailable",
					PublicMessage: "The upstream proxy is temporarily unavailable.", Cause: proxyErr,
				}
				return nil, commit, classified, nil
			}
			// ReasonProxyRejected (and any other reason) keeps the pre-existing
			// short-circuit behaviour: the proxy already refused the request with an
			// HTTP answer, so there is nothing to retry. Returning the raw error here
			// makes Run surface it as a 502 without a key switch (audit R5).
			return nil, commit, nil, executeErr
		}
	}
	if executeErr == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if _, err := a.states.MarkSuccess(stateCtx, lease.KeyID()); err != nil {
			// Fail-open (audit #29): the upstream already produced a real 2xx
			// body for the client. A best-effort key-state write must never turn
			// that success into a 5xx — the lease/scheduling row just recovers on
			// the next successful attempt. Only a cancelled stateCtx (client
			// disconnect) keeps the 499 semantics, because there is no longer a
			// client to deliver to.
			if stateErr := stateCtx.Err(); stateErr != nil && errors.Is(err, stateErr) {
				closeResponse(response)
				classified := fault.Classify(nil, stateErr, false, a.clock.Now())
				return nil, commit, &classified, nil
			}
			keepLease = true
			return response, commit, nil, nil
		}
		a.stateSync.ApplySuccess(lease.KeyID())
		keepLease = true
		return response, commit, nil, nil
	}

	classified := fault.Classify(response, executeErr, false, a.clock.Now())
	closeResponse(response)
	persisted, err := a.states.MarkFailure(stateCtx, lease.KeyID(), modelID, classified)
	if err != nil {
		if stateErr := stateCtx.Err(); stateErr != nil && errors.Is(err, stateErr) {
			return nil, commit, &classified, nil
		}
		return nil, commit, nil, fmt.Errorf("persist failed NVIDIA key state: %w", err)
	}
	a.stateSync.ApplyFailure(lease.KeyID(), modelID, classified, persisted)
	return nil, commit, &classified, nil
}

func (a *Attempt) budgetExpired(ctx context.Context) bool {
	return ctx.Err() != nil
}

const (
	retryBackoffBase = 100 * time.Millisecond
	retryBackoffMax  = 2 * time.Second
	// retryAfterMaxDelay caps how long a single backoff honours the upstream's
	// Retry-After hint. A misconfigured proxy or an account-level cooldown of
	// hours must not pin the retry loop to its whole value: the retry window
	// itself still bounds the loop.
	retryAfterMaxDelay = 5 * time.Minute
)

// backoff waits before the next key attempt. The delay doubles per attempt and
// carries the same 0.8-1.2 jitter factor fault.CalculateCooldown uses, so
// concurrent requests failing on the same upstream do not re-synchronise into
// bursts. A Retry-After hint on the current fault (typically a 429) replaces
// the exponential delay when it is longer, so an account-level rate limit is
// not retried before the upstream says it is ready. It never sleeps past the
// retry deadline, and returns the context error if the client disconnects
// while waiting.
func (a *Attempt) backoff(ctx context.Context, attempts int, budget Budget, currentFault *fault.Fault) error {
	// A valid upstream Retry-After is authoritative even on the first key
	// switch: account- or egress-level limits are not fixed by changing keys.
	// Preserve the fast path for ordinary per-key failures without a hint.
	if attempts <= 1 && (currentFault == nil || currentFault.RetryAfter <= 0) {
		return nil
	}
	delay := retryBackoffBase
	if attempts > 1 {
		delay = retryBackoffBase << (attempts - 2)
	}
	if delay > retryBackoffMax || delay <= 0 {
		delay = retryBackoffMax
	}
	delay = time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
	if currentFault != nil && currentFault.RetryAfter > 0 {
		suggested := currentFault.RetryAfter
		if suggested > retryAfterMaxDelay {
			suggested = retryAfterMaxDelay
		}
		if suggested > delay {
			delay = suggested
		}
	}
	if !budget.retryDeadline.IsZero() {
		if remaining := budget.retryDeadline.Sub(a.clock.Now()); remaining < delay {
			delay = remaining
		}
	}
	if delay <= 0 {
		return nil
	}
	timer := a.clock.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func chooseAcquireError(err error, lastFault *fault.Fault) error {
	// A client disconnect during acquire surfaces as a wrapped
	// context.Canceled. Without this check the handler falls through to the
	// 500 internal_error fallback and the disconnect gets billed as an
	// upstream failure in observability. fold the cancellation into a 499
	// fault here so writeChatError's fault.As branch maps it to the public
	// request_canceled envelope, matching the post-acquire code path that
	// already runs through fault.Classify.
	if errors.Is(err, context.Canceled) {
		return fault.Classify(nil, err, false, time.Time{})
	}
	if lastFault == nil {
		return fmt.Errorf("acquire NVIDIA key: %w", err)
	}
	var publicError *apierror.Error
	// Every candidate was tried and faulted; the pool has nothing else to offer
	// (disabled, cooling down, or busy-with-no-alternative). Surface the last
	// fault rather than an opaque acquire error so the caller sees the real
	// upstream outcome.
	if errors.As(err, &publicError) && (publicError.Code == "no_available_keys" || publicError.Code == "all_keys_cooling_down") {
		return *lastFault
	}
	return fmt.Errorf("acquire NVIDIA key: %w", err)
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}
