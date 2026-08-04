package router

import (
	"context"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

type Budget struct {
	connectTimeout    time.Duration
	firstByteTimeout  time.Duration
	firstByteDeadline time.Time
	totalDeadline     time.Time
	// retryDeadline bounds the pre-commit acquire/retry loop only. A stream has
	// no totalDeadline because its body legitimately runs for minutes, but that
	// previously left the retry loop itself unbounded: a stream could walk the
	// entire key pool with no time limit. This deadline stops the loop without
	// ever truncating a committed stream.
	retryDeadline time.Time
	// maxAttempts caps how many keys one request may try. Without it the bound
	// was the pool size, so a large pool turned one client request into that
	// many upstream calls.
	maxAttempts int
}

func newBudget(settings runtimeconfig.Snapshot, now time.Time, stream bool) Budget {
	// Mirror pool.resolveQueueSettings: the persisted settings range from
	// 1000ms up, but a zero-valued Snapshot reaching this path (uninitialised
	// provider, tests that never set a field) would otherwise produce 0
	// connect/first-byte timeouts and a deadline equal to `now` that fires
	// immediately. Treat anything <= 0 as unset and use the validator's 1s
	// floor; deliberately do NOT clamp small positive values, since tests push
	// sub-second budgets through this constructor on purpose.
	connectTimeoutMS := settings.ConnectTimeoutMS
	if connectTimeoutMS <= 0 {
		connectTimeoutMS = 1000
	}
	firstByteTimeoutMS := settings.FirstByteTimeoutMS
	if firstByteTimeoutMS <= 0 {
		firstByteTimeoutMS = 1000
	}
	firstByteTimeout := time.Duration(firstByteTimeoutMS) * time.Millisecond
	budget := Budget{
		connectTimeout:   time.Duration(connectTimeoutMS) * time.Millisecond,
		firstByteTimeout: firstByteTimeout,
	}
	if !stream {
		totalTimeoutMS := settings.NonstreamTotalTimeoutMS
		if totalTimeoutMS <= 0 {
			totalTimeoutMS = 1000
		}
		budget.totalDeadline = now.Add(time.Duration(totalTimeoutMS) * time.Millisecond)
	}
	retryBudgetMS := settings.RetryBudgetMS
	if retryBudgetMS <= 0 {
		retryBudgetMS = defaultRetryBudgetMS
	}
	budget.retryDeadline = now.Add(time.Duration(retryBudgetMS) * time.Millisecond)
	// For a non-stream request the total deadline already bounds everything, so
	// never let the retry window extend past it.
	if !budget.totalDeadline.IsZero() && budget.retryDeadline.After(budget.totalDeadline) {
		budget.retryDeadline = budget.totalDeadline
	}
	budget.maxAttempts = settings.MaxAttemptsPerRequest
	if budget.maxAttempts <= 0 {
		budget.maxAttempts = defaultMaxAttemptsPerRequest
	}
	return budget
}

const (
	defaultRetryBudgetMS         = 120000
	defaultMaxAttemptsPerRequest = 5
)

func (b Budget) ConnectTimeout() time.Duration {
	return b.connectTimeout
}

func (b Budget) FirstByteTimeout() time.Duration {
	return b.firstByteTimeout
}

func (b Budget) FirstByteDeadline() time.Time {
	return b.firstByteDeadline
}

func (b Budget) TotalDeadline() time.Time {
	return b.totalDeadline
}

// RetryDeadline bounds the pre-commit acquire/retry loop. It deliberately does
// not bound a committed stream: a long generation is legitimate, while an
// unbounded retry loop is not.
func (b Budget) RetryDeadline() time.Time {
	return b.retryDeadline
}

// MaxAttempts caps how many keys one request may burn. Without it the ceiling
// was the whole pool, so a single request against a degraded upstream could
// walk every key and multiply one client failure by the pool size.
func (b Budget) MaxAttempts() int {
	return b.maxAttempts
}

func (b Budget) forAttempt(now time.Time) Budget {
	b.firstByteDeadline = now.Add(b.firstByteTimeout)
	return b
}

type budgetContextKey struct{}

func withBudget(ctx context.Context, budget Budget) context.Context {
	return context.WithValue(ctx, budgetContextKey{}, budget)
}

func BudgetFromContext(ctx context.Context) (Budget, bool) {
	budget, ok := ctx.Value(budgetContextKey{}).(Budget)
	return budget, ok
}
