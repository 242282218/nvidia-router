package modelhealth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

type Status string

const (
	StatusHealthy      Status = "healthy"
	StatusDegraded     Status = "degraded"
	StatusUnavailable  Status = "unavailable"
	StatusUnchecked    Status = "unchecked"
	StatusStale        Status = "stale"
	StatusUnconfigured Status = "unconfigured"
)

const (
	OutcomeSuccess  = "success"
	OutcomeFailure  = "failure"
	OutcomeTimeout  = "timeout"
	OutcomeSkipped  = "skipped"
	OutcomeCanceled = "canceled"
	OutcomeMixed    = "mixed"
	OutcomeEmpty    = "empty"
)

const (
	DefaultIntervalSeconds = 60
	MinIntervalSeconds     = 10
	MaxIntervalSeconds     = 3600
	DefaultConcurrency     = 2
	MinConcurrency         = 1
	MaxConcurrency         = 8
)

var ErrInvalidSettings = errors.New("invalid model health settings")

type Settings struct {
	Enabled         bool      `json:"enabled"`
	IntervalSeconds int       `json:"interval_seconds"`
	Concurrency     int       `json:"concurrency"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SettingsPatch struct {
	Enabled         *bool
	IntervalSeconds *int
	Concurrency     *int
}

func DefaultSettings() Settings {
	return Settings{IntervalSeconds: DefaultIntervalSeconds, Concurrency: DefaultConcurrency}
}

func ValidateSettings(settings Settings) error {
	if settings.IntervalSeconds < MinIntervalSeconds || settings.IntervalSeconds > MaxIntervalSeconds {
		return fmt.Errorf("%w: interval_seconds must be between %d and %d", ErrInvalidSettings, MinIntervalSeconds, MaxIntervalSeconds)
	}
	if settings.Concurrency < MinConcurrency || settings.Concurrency > MaxConcurrency {
		return fmt.Errorf("%w: concurrency must be between %d and %d", ErrInvalidSettings, MinConcurrency, MaxConcurrency)
	}
	return nil
}

type ProbeEvent struct {
	ID         int64     `json:"id,omitempty"`
	ModelID    int64     `json:"model_id"`
	Outcome    string    `json:"outcome"`
	DurationMS int64     `json:"duration_ms"`
	ErrorCode  string    `json:"error_code,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Latest struct {
	Outcome             string
	DurationMS          int64
	ErrorCode           string
	LastProbeAt         time.Time
	ConsecutiveFailures int
}

type WindowStats struct {
	ProbeCount   int64
	SuccessCount int64
	FailureCount int64
	TimeoutCount int64
}

type Bucket struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Outcome      string    `json:"outcome"`
	ProbeCount   int64     `json:"probe_count"`
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
	TimeoutCount int64     `json:"timeout_count"`
	AverageMS    float64   `json:"average_duration_ms"`
}

func ClassifyStatus(latest *Latest, stats WindowStats, now time.Time, interval time.Duration) Status {
	if latest == nil || latest.LastProbeAt.IsZero() || stats.ProbeCount == 0 {
		return StatusUnchecked
	}
	if interval <= 0 {
		interval = time.Duration(DefaultIntervalSeconds) * time.Second
	}
	if latest.LastProbeAt.Add(2 * interval).Before(now) {
		return StatusStale
	}
	if latest.Outcome == OutcomeSkipped {
		return StatusUnconfigured
	}
	if latest.ConsecutiveFailures >= 3 || stats.SuccessCount == 0 {
		return StatusUnavailable
	}
	if stats.FailureCount > 0 || stats.TimeoutCount > 0 {
		return StatusDegraded
	}
	return StatusHealthy
}

func BuildBuckets(from, to time.Time, count int, events []ProbeEvent) []Bucket {
	if count <= 0 || !to.After(from) {
		return nil
	}
	from = from.UTC()
	to = to.UTC()
	step := to.Sub(from) / time.Duration(count)
	buckets := make([]Bucket, count)
	for index := range buckets {
		start := from.Add(time.Duration(index) * step)
		buckets[index] = Bucket{Start: start, End: start.Add(step), Outcome: OutcomeEmpty}
	}
	for _, event := range events {
		created := event.CreatedAt.UTC()
		if created.Before(from) || !created.Before(to) {
			continue
		}
		index := int(created.Sub(from) / step)
		if index < 0 || index >= len(buckets) {
			continue
		}
		bucket := &buckets[index]
		bucket.ProbeCount++
		bucket.AverageMS += float64(event.DurationMS)
		switch event.Outcome {
		case OutcomeSuccess:
			bucket.SuccessCount++
		case OutcomeTimeout:
			bucket.TimeoutCount++
		case OutcomeFailure:
			bucket.FailureCount++
		}
	}
	for index := range buckets {
		bucket := &buckets[index]
		if bucket.ProbeCount == 0 {
			continue
		}
		bucket.AverageMS /= float64(bucket.ProbeCount)
		bucket.Outcome = bucketOutcome(*bucket)
	}
	return buckets
}

func bucketOutcome(bucket Bucket) string {
	if bucket.ProbeCount == 0 {
		return OutcomeEmpty
	}
	if bucket.SuccessCount == bucket.ProbeCount {
		return OutcomeSuccess
	}
	if bucket.SuccessCount == 0 {
		switch {
		case bucket.TimeoutCount == bucket.ProbeCount:
			return OutcomeTimeout
		case bucket.FailureCount == bucket.ProbeCount:
			return OutcomeFailure
		}
	}
	return OutcomeMixed
}

func ClassifyProbeError(err error) string {
	if err == nil {
		return OutcomeSuccess
	}
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return OutcomeTimeout
	}
	return OutcomeFailure
}

func SafeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	var netError net.Error
	if errors.As(err, &netError) {
		return "network_error"
	}
	return "probe_failed"
}
