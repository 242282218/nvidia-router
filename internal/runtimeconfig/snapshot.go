package runtimeconfig

import (
	"fmt"
	"strings"
	"time"

	"nvidia-router/internal/fault"
)

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
