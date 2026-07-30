package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/runtimeconfig"
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
	Attempts int
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

	attempted := make(map[int64]struct{})
	var lastFault *fault.Fault
	for {
		attemptBudget := budget.forAttempt(a.clock.Now())
		lease, err := a.acquire(requestCtx, attemptBudget, settings, modelID, attempted)
		if err != nil {
			if lastFault != nil && (a.budgetExpired(requestCtx) || !a.clock.Now().Before(attemptBudget.FirstByteDeadline())) {
				return AttemptResult{}, *lastFault
			}
			return AttemptResult{}, chooseAcquireError(err, lastFault)
		}
		attempted[lease.KeyID()] = struct{}{}

		executeCtx := withBudget(requestCtx, attemptBudget)
		response, commit, currentFault, err := a.executeLease(executeCtx, ctx, modelID, lease, execute)
		if err != nil {
			return AttemptResult{}, err
		}
		if currentFault == nil {
			return AttemptResult{Response: response, Lease: lease, Attempts: len(attempted)}, nil
		}
		lastFault = currentFault
		if !currentFault.Retryable || commit.Committed() || a.budgetExpired(requestCtx) {
			return AttemptResult{}, *currentFault
		}
	}
}

func (a *Attempt) requestContext(ctx context.Context, budget Budget) (context.Context, context.CancelFunc) {
	if budget.totalDeadline.IsZero() {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, budget.totalDeadline)
}

func (a *Attempt) acquire(
	ctx context.Context,
	budget Budget,
	settings runtimeconfig.Snapshot,
	modelID int64,
	attempted map[int64]struct{},
) (pool.Lease, error) {
	if !a.clock.Now().Before(budget.FirstByteDeadline()) {
		return nil, fault.Classify(nil, context.DeadlineExceeded, false, a.clock.Now())
	}
	acquireCtx, cancel := context.WithDeadline(ctx, budget.FirstByteDeadline())
	defer cancel()
	lease, err := a.keyPool.AcquireWithSnapshot(acquireCtx, modelID, attempted, settings)
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
	if executeErr == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if _, err := a.states.MarkSuccess(stateCtx, lease.KeyID()); err != nil {
			closeResponse(response)
			return nil, commit, nil, fmt.Errorf("persist successful NVIDIA key state: %w", err)
		}
		a.stateSync.ApplySuccess(lease.KeyID())
		keepLease = true
		return response, commit, nil, nil
	}

	classified := fault.Classify(response, executeErr, false, a.clock.Now())
	closeResponse(response)
	persisted, err := a.states.MarkFailure(stateCtx, lease.KeyID(), modelID, classified)
	if err != nil {
		return nil, commit, nil, fmt.Errorf("persist failed NVIDIA key state: %w", err)
	}
	a.stateSync.ApplyFailure(lease.KeyID(), modelID, classified, persisted)
	return nil, commit, &classified, nil
}

func (a *Attempt) budgetExpired(ctx context.Context) bool {
	return ctx.Err() != nil
}

func chooseAcquireError(err error, lastFault *fault.Fault) error {
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
