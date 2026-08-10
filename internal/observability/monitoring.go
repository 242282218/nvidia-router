package observability

import (
	"context"
	"errors"
	"time"
)

const (
	MonitoringRange24Hours = "24h"
	MonitoringRange7Days   = "7d"
	MonitoringRange30Days  = "30d"
	MaxMonitoringPageSize  = 100
)

type MonitoringFilter struct {
	ModelID     string
	Endpoint    string
	Outcome     string
	Search      string
	Status      *int
	AccessKeyID *int64
	NVIDIAKeyID *int64
}

type MonitoringQuery struct {
	Range  string
	From   time.Time
	To     time.Time
	Filter MonitoringFilter
}

// MetricsSummary is the aggregate, label-free subset safe to expose on the
// unauthenticated Prometheus endpoint. It contains no model, key, or request IDs.
type MetricsSummary struct {
	Requests  int64
	Successes int64
	Failures  int64
}

type MonitoringSummary struct {
	RequestCount        int64   `json:"request_count"`
	SuccessCount        int64   `json:"success_count"`
	FailureCount        int64   `json:"failure_count"`
	SuccessRate         float64 `json:"success_rate"`
	AverageDurationMS   float64 `json:"average_duration_ms"`
	AverageFirstByteMS  float64 `json:"average_first_byte_ms"`
	AverageFirstTokenMS float64 `json:"average_first_token_ms"`
	AverageQueueMS      float64 `json:"average_queue_ms"`
	TotalAttempts       int64   `json:"total_attempts"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens"`
	// FirstTokenP50MS / FirstTokenP95MS are true nearest-rank quantiles over the
	// first_token_ms samples in the window, not aggregates of the daily_stats
	// totals (which only keep sum+count and cannot reproduce percentiles). They
	// are absent while no streaming request in the window produced a token.
	FirstTokenP50MS *int64 `json:"first_token_p50_ms,omitempty"`
	FirstTokenP95MS *int64 `json:"first_token_p95_ms,omitempty"`
}

type MonitoringSeriesPoint struct {
	Bucket              string  `json:"bucket"`
	RequestCount        int64   `json:"request_count"`
	SuccessCount        int64   `json:"success_count"`
	FailureCount        int64   `json:"failure_count"`
	AverageDurationMS   float64 `json:"average_duration_ms"`
	AverageFirstByteMS  float64 `json:"average_first_byte_ms"`
	AverageFirstTokenMS float64 `json:"average_first_token_ms"`
	AverageQueueMS      float64 `json:"average_queue_ms"`
	TotalAttempts       int64   `json:"total_attempts"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens"`
}

type MonitoringSnapshot struct {
	Range   string                  `json:"range"`
	From    time.Time               `json:"from"`
	To      time.Time               `json:"to"`
	Summary MonitoringSummary       `json:"summary"`
	Series  []MonitoringSeriesPoint `json:"series"`
}

type RequestLog struct {
	RequestID         string  `json:"request_id"`
	Endpoint          string  `json:"endpoint"`
	ModelID           *string `json:"model_id,omitempty"`
	AccessKeyID       *int64  `json:"access_key_id,omitempty"`
	NVIDIAKeyID       *int64  `json:"nvidia_key_id,omitempty"`
	HTTPStatus        int     `json:"http_status"`
	Outcome           string  `json:"outcome"`
	ErrorCode         *string `json:"error_code,omitempty"`
	IsStream          bool    `json:"is_stream"`
	QueueMS           int64   `json:"queue_ms"`
	FirstByteMS       *int64  `json:"first_byte_ms,omitempty"`
	FirstTokenMS      *int64  `json:"first_token_ms,omitempty"`
	DurationMS        int64   `json:"duration_ms"`
	AttemptCount      int64   `json:"attempt_count"`
	PromptTokens      *int64  `json:"prompt_tokens,omitempty"`
	CompletionTokens  *int64  `json:"completion_tokens,omitempty"`
	UpstreamRequestID *string `json:"upstream_request_id,omitempty"`
	CreatedAt         string  `json:"created_at"`
}

type RequestLogsQuery struct {
	MonitoringQuery
	Page     int
	PageSize int
}

type RequestLogsPage struct {
	Items    []RequestLog `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Total    int64        `json:"total"`
	HasMore  bool         `json:"has_more"`
}

type MonitoringStore interface {
	MonitoringSummary(context.Context, MonitoringQuery) (MonitoringSnapshot, error)
	ListRequestLogs(context.Context, RequestLogsQuery) (RequestLogsPage, error)
}

func NewMonitoringQuery(now time.Time, rangeName string, filter MonitoringFilter) (MonitoringQuery, error) {
	now = now.UTC()
	if now.IsZero() {
		return MonitoringQuery{}, errors.New("monitoring query time is required")
	}
	query := MonitoringQuery{Range: rangeName, To: now, Filter: filter}
	switch rangeName {
	case MonitoringRange24Hours:
		query.From = now.Truncate(time.Hour).Add(-23 * time.Hour)
	case MonitoringRange7Days:
		query.From = startOfUTCDay(now).AddDate(0, 0, -6)
	case MonitoringRange30Days:
		query.From = startOfUTCDay(now).AddDate(0, 0, -29)
	default:
		return MonitoringQuery{}, errors.New("monitoring range is invalid")
	}
	return query, nil
}

func startOfUTCDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
