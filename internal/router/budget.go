package router

import (
	"context"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

type Budget struct {
	connectTimeout    time.Duration
	firstByteDeadline time.Time
	totalDeadline     time.Time
}

func newBudget(settings runtimeconfig.Snapshot, now time.Time, stream bool) Budget {
	budget := Budget{
		connectTimeout:    time.Duration(settings.ConnectTimeoutMS) * time.Millisecond,
		firstByteDeadline: now.Add(time.Duration(settings.FirstByteTimeoutMS) * time.Millisecond),
	}
	if !stream {
		budget.totalDeadline = now.Add(time.Duration(settings.NonstreamTotalTimeoutMS) * time.Millisecond)
	}
	return budget
}

func (b Budget) ConnectTimeout() time.Duration {
	return b.connectTimeout
}

func (b Budget) FirstByteDeadline() time.Time {
	return b.firstByteDeadline
}

type budgetContextKey struct{}

func withBudget(ctx context.Context, budget Budget) context.Context {
	return context.WithValue(ctx, budgetContextKey{}, budget)
}

func BudgetFromContext(ctx context.Context) (Budget, bool) {
	budget, ok := ctx.Value(budgetContextKey{}).(Budget)
	return budget, ok
}
