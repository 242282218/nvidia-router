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
	// firstTokenDeadline bounds the pre-commit wait for the first SSE data event
	// on a streaming request. It is set alongside firstByteDeadline in forAttempt
	// from streamFirstTokenTimeout; non-stream budgets leave it zero and
	// FirstTokenDeadline falls back to the first-byte deadline.
	firstTokenDeadline time.Time
	totalDeadline      time.Time
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
	// streamFirstTokenTimeout is the pre-commit first-token window for streaming
	// requests (runtimeconfig.StreamFirstTokenTimeoutMS). Zero on non-stream
	// budgets, which keep firstByteTimeout as their only pre-commit window.
	streamFirstTokenTimeout time.Duration
	// streamIdleTimeout bounds silence between SSE data events once a stream is
	// committed, so a stalled upstream cannot pin the lease forever while a
	// slow-but-live generation is not truncated.
	streamIdleTimeout time.Duration
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
	// The stream idle guard is resolved for every budget even though only
	// streaming handlers consume it, so StreamIdleTimeout never returns a zero
	// that would silently disable the wrap (WithIdleTimeout passes bodies
	// through unchanged on a non-positive idle).
	streamIdleTimeoutMS := settings.StreamIdleTimeoutMS
	if streamIdleTimeoutMS <= 0 {
		streamIdleTimeoutMS = defaultStreamIdleTimeoutMS
	}
	budget.streamIdleTimeout = time.Duration(streamIdleTimeoutMS) * time.Millisecond
	// Streams split the pre-commit window: firstByteTimeout keeps bounding the
	// transport's header wait, while streamFirstTokenTimeout bounds the prime
	// phase until the first SSE data event arrives.
	if stream {
		firstTokenTimeoutMS := settings.StreamFirstTokenTimeoutMS
		if firstTokenTimeoutMS <= 0 {
			firstTokenTimeoutMS = defaultStreamFirstTokenTimeoutMS
		}
		budget.streamFirstTokenTimeout = time.Duration(firstTokenTimeoutMS) * time.Millisecond
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
	// Zero-value stream timeouts fall back to the migration 014 documented
	// defaults (60000/180000), NOT the validator's 1s floor. A snapshot that has
	// not been through the migration carries 0 for both columns, and snapshot.go
	// promises the budget layer resolves 0 to the documented defaults; a 1s
	// stream idle window would truncate any slow-but-live generation, which is
	// exactly the false-positive truncation the 014 split was designed to avoid.
	defaultStreamFirstTokenTimeoutMS = 60000
	defaultStreamIdleTimeoutMS       = 180000
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

// FirstTokenDeadline bounds the pre-commit wait for the first SSE data event.
// Streaming budgets carry their own window (stream_first_token_timeout_ms);
// non-stream budgets fall back to the first-byte deadline, which is the only
// pre-commit window they have.
func (b Budget) FirstTokenDeadline() time.Time {
	if !b.firstTokenDeadline.IsZero() {
		return b.firstTokenDeadline
	}
	return b.firstByteDeadline
}

// StreamIdleTimeout bounds silence between SSE data events once a streaming
// response is committed. Non-stream budgets never consume it.
func (b Budget) StreamIdleTimeout() time.Duration {
	return b.streamIdleTimeout
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
	if deadline := b.preCommitDeadline(); !deadline.IsZero() && deadline.Before(b.firstByteDeadline) {
		b.firstByteDeadline = deadline
	}
	// Streams additionally pin the first-token deadline; the prime phase waits
	// on this window instead of the transport-level first-byte window.
	if b.streamFirstTokenTimeout > 0 {
		b.firstTokenDeadline = now.Add(b.streamFirstTokenTimeout)
		if deadline := b.preCommitDeadline(); !deadline.IsZero() && deadline.Before(b.firstTokenDeadline) {
			b.firstTokenDeadline = deadline
		}
	}
	return b
}

// preCommitDeadline is the earliest request deadline that may stop an attempt
// before a response is committed. A non-stream request also has a total
// deadline; retry_budget_ms is intentionally included because an in-flight
// attempt must not continue after the retry window has expired.
func (b Budget) preCommitDeadline() time.Time {
	deadline := b.retryDeadline
	if !b.totalDeadline.IsZero() && (deadline.IsZero() || b.totalDeadline.Before(deadline)) {
		deadline = b.totalDeadline
	}
	return deadline
}

type budgetContextKey struct{}

func withBudget(ctx context.Context, budget Budget) context.Context {
	return context.WithValue(ctx, budgetContextKey{}, budget)
}

func BudgetFromContext(ctx context.Context) (Budget, bool) {
	budget, ok := ctx.Value(budgetContextKey{}).(Budget)
	return budget, ok
}
