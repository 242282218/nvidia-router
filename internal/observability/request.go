package observability

import "time"

const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	// OutcomeCanceled is a client-side cancel (HTTP 499). It is intentionally
	// not counted as failure so NVIDIA TTFT-driven 499s do not drag the success
	// rate down; the separate http_status/error_code dimension keeps it visible.
	OutcomeCanceled = "canceled"
)

const (
	DimensionGlobal    = "global"
	DimensionModel     = "model"
	DimensionNVIDIAKey = "nvidia_key"
	DimensionAccessKey = "access_key"
	GlobalDimensionID  = "all"
)

// RequestRecord intentionally contains only metadata allowed for persistence.
type RequestRecord struct {
	RequestID         string
	Endpoint          string
	ModelID           string
	AccessKeyID       *int64
	NVIDIAKeyID       *int64
	HTTPStatus        int
	Outcome           string
	ErrorCode         *string
	IsStream          bool
	QueueMS           int64
	FirstByteMS       *int64
	FirstTokenMS      *int64
	DurationMS        int64
	AttemptCount      int
	PromptTokens      *int64
	CompletionTokens  *int64
	UpstreamRequestID *string
	// Reasoning observability carries only booleans, field names and character
	// counts. Reasoning text, prompts and keys are never persisted.
	ReasoningRequested      bool
	ReasoningWireFields     string
	ReasoningSource         string
	ReasoningRequestedLevel string
	ReasoningEffectiveLevel string
	ReasoningPresent        bool
	ReasoningChars          *int64
	StreamDone              bool
	RouteMode               string
	CreatedAt               time.Time
}

type DailyStat struct {
	Day                 string  `json:"day"`
	DimensionType       string  `json:"dimension_type"`
	DimensionID         string  `json:"dimension_id"`
	RequestCount        int64   `json:"request_count"`
	SuccessCount        int64   `json:"success_count"`
	FailureCount        int64   `json:"failure_count"`
	CanceledCount       int64   `json:"canceled_count"`
	AverageDuration     float64 `json:"average_duration_ms"`
	AverageQueue        float64 `json:"average_queue_ms"`
	AverageAttempts     float64 `json:"average_attempts"`
	AverageFirstByteMS  float64 `json:"average_first_byte_ms"`
	AverageFirstTokenMS float64 `json:"average_first_token_ms"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens"`
}

type RecentError struct {
	RequestID         string  `json:"request_id"`
	Endpoint          string  `json:"endpoint"`
	ModelID           *string `json:"model_id,omitempty"`
	NVIDIAKeyID       *int64  `json:"nvidia_key_id,omitempty"`
	AccessKeyID       *int64  `json:"access_key_id,omitempty"`
	HTTPStatus        int     `json:"http_status"`
	ErrorCode         string  `json:"error_code"`
	UpstreamRequestID *string `json:"upstream_request_id,omitempty"`
	CreatedAt         string  `json:"created_at"`
}
