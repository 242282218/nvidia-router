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
	return budget
}

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
