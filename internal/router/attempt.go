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

type AttemptResult struct {
	Response *http.Response
	Lease    pool.Lease
	Commit   *CommitState
	Attempts int
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
}

func NewAttempt(
	settings runtimeconfig.Provider,
	keyPool KeyPool,
	secrets SecretProvider,
	states KeyStateWriter,
	stateSync StateSync,
	source clock.Clock,
) *Attempt {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Attempt{
		settings:  settings,
		keyPool:   keyPool,
		secrets:   secrets,
		states:    states,
		stateSync: stateSync,
		clock:     source,
	}
}

func (a *Attempt) Run(ctx context.Context, modelID int64, stream bool, execute ExecuteFunc) (AttemptResult, error) {
	now := a.clock.Now()
	settings := a.settings.Snapshot()
	budget := newBudget(settings, now, stream)
	requestCtx, cancel := a.requestContext(ctx, budget)
	defer cancel()

	// Build the failover matcher once per request: the spec lives on the same
	// snapshot the rest of the budget reads, so decoding it during Run keeps the
	// retry policy consistent with the timeouts above (audit B4).
	failover := buildFailoverMatcher(settings.FailoverStatusCodes)

	attempted := make(map[int64]struct{})
	var totalQueue time.Duration
	var lastFault *fault.Fault
	for {
		queueStarted := a.clock.Now()
		lease, err := a.acquire(requestCtx, settings, modelID, attempted)
		totalQueue += a.clock.Now().Sub(queueStarted)
		if err != nil {
			if lastFault != nil && a.budgetExpired(requestCtx) {
				return AttemptResult{}, *lastFault
			}
			return AttemptResult{}, chooseAcquireError(err, lastFault)
		}
		attempted[lease.KeyID()] = struct{}{}
		observability.SetAttempt(ctx, lease.KeyID(), len(attempted), totalQueue)

		// The first-byte budget starts once a lease is acquired; queue time is
		// bounded separately by the pool's queue-wait setting (queue_timeout).
		executeCtx := withBudget(requestCtx, budget.forAttempt(a.clock.Now()))
		response, commit, currentFault, err := a.executeLease(executeCtx, requestCtx, modelID, lease, execute)
		if err != nil {
			return AttemptResult{}, err
		}
		if currentFault == nil {
			observability.SetUpstreamRequestID(ctx, response.Header.Get("X-Request-ID"))
			return AttemptResult{Response: response, Lease: lease, Commit: commit, Attempts: len(attempted)}, nil

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
		if err := a.backoff(requestCtx, len(attempted), budget); err != nil {
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
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, budget.totalDeadline)
}

func (a *Attempt) acquire(
	ctx context.Context,
	settings runtimeconfig.Snapshot,
	modelID int64,
	attempted map[int64]struct{},
) (pool.Lease, error) {
	lease, err := a.keyPool.AcquireWithSnapshot(ctx, modelID, attempted, settings)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fault.Classify(nil, err, false, a.clock.Now())
	}
	return lease, err
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
)

// backoff waits before the next key attempt. The delay doubles per attempt and
// carries the same 0.8-1.2 jitter factor fault.CalculateCooldown uses, so
// concurrent requests failing on the same upstream do not re-synchronise into
// bursts. It never sleeps past the retry deadline, and returns the context
// error if the client disconnects while waiting.
func (a *Attempt) backoff(ctx context.Context, attempts int, budget Budget) error {
	// The first retry is immediate. One bad key is the common case, and moving to
	// a different key is not the same as hammering one endpoint, so paying a
	// delay there would slow down every ordinary failover. Escalate only once
	// failures repeat, which is the signal that the upstream itself is unwell.
	if attempts <= 1 {
		return nil
	}
	delay := retryBackoffBase << (attempts - 2)
	if delay > retryBackoffMax || delay <= 0 {
		delay = retryBackoffMax
	}
	delay = time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
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
	if errors.As(err, &publicError) && publicError.Code == "no_available_keys" {
		return *lastFault
	}
	return fmt.Errorf("acquire NVIDIA key: %w", err)
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}
