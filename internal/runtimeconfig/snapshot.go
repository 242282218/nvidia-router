package runtimeconfig

import "fmt"

// Snapshot is the runtime configuration read once at the beginning of a request.
type Snapshot struct {
	QueueCapacity           int
	QueueWaitTimeoutMS      int
	ConnectTimeoutMS        int
	FirstByteTimeoutMS      int
	NonstreamTotalTimeoutMS int
	ShutdownGraceMS         int
}

// ValidationError identifies the setting that violates the database range check.
type ValidationError struct {
	Field string
	Min   int
	Max   int
	Value int
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s must be between %d and %d", e.Field, e.Min, e.Max)
}

// Validate mirrors the CHECK constraints in the runtime_settings table.
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
	}
	for _, check := range checks {
		if check.value < check.min || check.value > check.max {
			return &ValidationError{Field: check.field, Min: check.min, Max: check.max, Value: check.value}
		}
	}
	return nil
}

// Provider exposes an immutable configuration snapshot to request handlers.
type Provider interface {
	Snapshot() Snapshot
}
