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
	firstByteTimeout := time.Duration(settings.FirstByteTimeoutMS) * time.Millisecond
	budget := Budget{
		connectTimeout:   time.Duration(settings.ConnectTimeoutMS) * time.Millisecond,
		firstByteTimeout: firstByteTimeout,
	}
	if !stream {
		budget.totalDeadline = now.Add(time.Duration(settings.NonstreamTotalTimeoutMS) * time.Millisecond)
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
