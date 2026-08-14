package runtimeconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nvidia-router/internal/fault"
)

// ModelTimeouts carries per-model streaming timeout overrides that the handler
// layer injects into the request context after resolving the model. A zero
// value for either field means "use the global setting from the Snapshot".
type ModelTimeouts struct {
	StreamFirstTokenTimeoutMS int
	StreamIdleTimeoutMS       int
}

type modelTimeoutKey struct{}

// WithModelTimeouts stores per-model timeout hints in the context so the
// router's budget builder can apply them without changing the Attempt.Run
// signature. Callers set this after resolving the model; the router reads it
// before constructing the Budget.
func WithModelTimeouts(ctx context.Context, hints ModelTimeouts) context.Context {
	return context.WithValue(ctx, modelTimeoutKey{}, hints)
}

// ModelTimeoutsFromContext retrieves per-model timeout hints from the context,
// returning the zero value and false when none were set.
func ModelTimeoutsFromContext(ctx context.Context) (ModelTimeouts, bool) {
	hints, ok := ctx.Value(modelTimeoutKey{}).(ModelTimeouts)
	return hints, ok
}

// Snapshot is the runtime configuration read once at the beginning of a request.
type Snapshot struct {
	QueueCapacity           int
	QueueWaitTimeoutMS      int
	ConnectTimeoutMS        int
	FirstByteTimeoutMS      int
	NonstreamTotalTimeoutMS int
	ShutdownGraceMS         int
	// FailoverStatusCodes is the operator-tunable spec driving router.Attempt's
	// retry decision (audit B4). An empty string is the legacy sentinel: the
	// router falls back to fault.DefaultFailoverStatusCodes.
	FailoverStatusCodes string
	// RequestLogRetentionDays drives observability.CleanupWorker instead of the
	// previously hardcoded 30-day constant (audit B5).
	RequestLogRetentionDays int
	// MaxAttemptsPerRequest caps how many keys one request may try. Previously
	// the ceiling was the whole key pool, so a single request against a large
	// pool could amplify into hundreds of upstream calls.
	MaxAttemptsPerRequest int
	// RetryBudgetMS bounds the pre-commit retry phase. It deliberately does not
	// bound a committed stream: once the first byte reaches the client the
	// response is no longer retryable and a long generation is legitimate.
	RetryBudgetMS int
	// MaxStreamingPerKey is the per-key quota of concurrent streaming requests.
	// Streaming requests previously held the key's only busy slot for their whole
	// (potentially minute-long) lifetime, stalling short requests routed to that
	// key. The busy slot keeps serving short requests while streams draw from an
	// independent per-key budget instead (audit R4).
	MaxStreamingPerKey int
	// StreamFirstTokenTimeoutMS bounds the pre-commit wait for the first SSE
	// data event (TTFT) on a streaming request. It splits the old first-byte
	// window: first_byte_timeout_ms keeps bounding the transport's header wait,
	// while this setting bounds how long the prime phase may wait for the first
	// token before the attempt loop gives up.
	StreamFirstTokenTimeoutMS int
	// StreamIdleTimeoutMS bounds the silence between SSE data events once a
	// stream is committed. It replaces the reuse of first_byte_timeout_ms as the
	// in-stream idle guard, so a slow-but-live generation is not truncated by a
	// window sized for the first token.
	StreamIdleTimeoutMS int
	// LatencyRoutingEnabled toggles quality-aware scheduling. The legacy field
	// name is kept for API compatibility; when on, key scheduling uses live
	// request quality and the proxy pool uses request quality with latency as a
	// tie-breaker. Off restores the legacy round-robin behaviour.
	LatencyRoutingEnabled bool
	// EmbeddingCacheEnabled gates the in-memory exact-match cache for
	// /v1/embeddings. The cache is optional because it changes observable
	// behaviour (identical inputs may get a cached vector without an upstream
	// call), so operators can disable it explicitly.
	EmbeddingCacheEnabled bool
	// EmbeddingCacheMaxEntries bounds the in-memory embedding cache. A bounded
	// LRU keeps memory flat regardless of how many distinct inputs flow through.
	EmbeddingCacheMaxEntries int
	// FirstByteDeadline is request-local metadata and is intentionally not persisted.
	FirstByteDeadline time.Time
}

// ValidationError identifies the setting that violates the database range check.
type ValidationError struct {
	Field string
	Min   int
	Max   int
	Value int
	// StringValue carries the offending text payload for string-field validation
	// (see failover_status_codes). Numeric checks leave it empty.
	StringValue string
	// Cause wraps the parse error for string-field validation so admins see the
	// underlying reason ("non-numeric code", "range start > end", ...) instead of
	// an opaque "out of range" message.
	Cause error
}

func (e *ValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Field, e.Cause)
	}
	if e.StringValue != "" {
		return fmt.Sprintf("%s has an invalid value %q", e.Field, e.StringValue)
	}
	return fmt.Sprintf("%s must be between %d and %d", e.Field, e.Min, e.Max)
}

// Validate mirrors the CHECK constraints in the runtime_settings table.
//
// These bounds are the authoritative source; the front-end SettingsForm.vue
// duplicates a subset purely as an inline UX pre-check (audit #64). Keep both
// in sync — the server rejects out-of-range values regardless.
func Validate(snapshot Snapshot) error {
	checks := []struct {
		field string
		value int
		min   int
		max   int
	}{
		{"queue_capacity", snapshot.QueueCapacity, 1, 10000},
		{"queue_wait_timeout_ms", snapshot.QueueWaitTimeoutMS, 1000, 600000},
		{"connect_timeout_ms", snapshot.ConnectTimeoutMS, 1000, 120000},
		{"first_byte_timeout_ms", snapshot.FirstByteTimeoutMS, 1000, 600000},
		{"nonstream_total_timeout_ms", snapshot.NonstreamTotalTimeoutMS, 1000, 1800000},
		{"shutdown_grace_ms", snapshot.ShutdownGraceMS, 1000, 600000},
		{"request_log_retention_days", snapshot.RequestLogRetentionDays, 30, 365},
		{"max_attempts_per_request", snapshot.MaxAttemptsPerRequest, 1, 50},
		{"retry_budget_ms", snapshot.RetryBudgetMS, 1000, 600000},
	}
	// MaxStreamingPerKey deliberately skips the zero value: a snapshot built
	// before the migration landed carries 0 for the new column, and the pool
	// resolves 0 to the documented default of 2. Enforcing 1..10 on a
	// pre-migration row would reject every existing deployment at startup.
	if snapshot.MaxStreamingPerKey != 0 {
		checks = append(checks, struct {
			field string
			value int
			min   int
			max   int
		}{"max_streaming_per_key", snapshot.MaxStreamingPerKey, 1, 10})
	}
	// The stream timeout columns follow the same zero-skip convention: they are
	// added by migration 014 with NOT NULL defaults, but a snapshot that has not
	// been through the migration (or a test literal) carries 0 for both, and the
	// budget layer resolves 0 to the documented defaults.
	if snapshot.StreamFirstTokenTimeoutMS != 0 {
		checks = append(checks, struct {
			field string
			value int
			min   int
			max   int
		}{"stream_first_token_timeout_ms", snapshot.StreamFirstTokenTimeoutMS, 1000, 1800000})
	}
	if snapshot.StreamIdleTimeoutMS != 0 {
		checks = append(checks, struct {
			field string
			value int
			min   int
			max   int
		}{"stream_idle_timeout_ms", snapshot.StreamIdleTimeoutMS, 1000, 1800000})
	}
	if snapshot.EmbeddingCacheMaxEntries != 0 {
		checks = append(checks, struct {
			field string
			value int
			min   int
			max   int
		}{"embedding_cache_max_entries", snapshot.EmbeddingCacheMaxEntries, 1, 10000})
	}
	for _, check := range checks {
		if check.value < check.min || check.value > check.max {
			return &ValidationError{Field: check.field, Min: check.min, Max: check.max, Value: check.value}
		}
	}
	// An empty failover_status_codes is a legal operator choice ("never fail
	// over"); we let fault.NewFailoverMatcher accept it. Any other string is
	// validated by the parser so an admin typo surfaces here rather than at
	// request time when a bad spec would silently turn off failover.
	if strings.TrimSpace(snapshot.FailoverStatusCodes) != "" {
		if _, err := fault.NewFailoverMatcher(snapshot.FailoverStatusCodes); err != nil {
			return &ValidationError{Field: "failover_status_codes", StringValue: snapshot.FailoverStatusCodes, Cause: err}
		}
	}
	return nil
}

// Provider exposes an immutable configuration snapshot to request handlers.
type Provider interface {
	Snapshot() Snapshot
}
