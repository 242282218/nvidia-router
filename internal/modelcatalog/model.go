package modelcatalog

import (
	"errors"
	"fmt"
	"time"

	"nvidia-router/internal/compat"
)

var (
	ErrModelVersionConflict  = errors.New("model version conflict")
	ErrInvalidModelSelection = errors.New("invalid model selection")
)

const (
	ProviderNVIDIA       = "nvidia"
	ProviderOpenCodeFree = "opencodefree"
)

const defaultModelProvider = ProviderNVIDIA

type Kind string

const (
	KindChat      Kind = "chat"
	KindEmbedding Kind = "embedding"
	KindASR       Kind = "asr"
	KindTTS       Kind = "tts"
)

type Model struct {
	ID                      int64
	PublicID                string
	UpstreamID              string
	DisplayName             string
	Kind                    Kind
	Provider                string
	Enabled                 bool
	SupportsVision          bool
	SupportsTools           bool
	SupportsReasoning       bool
	ReasoningWireFormat     string
	ReasoningLevels         []string
	ReasoningMinBudget      int
	ReasoningMaxBudget      int
	ReasoningZeroAllowed    bool
	ReasoningDynamicAllowed bool
	CapabilityVerifiedAt    *time.Time
	// CreatedAt backs the OpenAI /v1/models "created" field. The column
	// already existed but was never read, so the handler substituted
	// time.Now() and the value changed on every request.
	CreatedAt time.Time
	// StreamFirstTokenTimeoutMS overrides the global stream_first_token_timeout_ms
	// for this model when non-nil. Used for models like deepseek-v4-flash whose
	// TTFT is much slower than the fleet-wide default.
	StreamFirstTokenTimeoutMS *int
	// StreamIdleTimeoutMS overrides the global stream_idle_timeout_ms for this
	// model when non-nil.
	StreamIdleTimeoutMS *int
	// InputUSDPerMTok / OutputUSDPerMTok are optional per-token prices (USD per
	// 1M tokens) used for cost estimation in the monitoring surface. A nil
	// value means the model is not priced and counts as $0.
	InputUSDPerMTok  *float64
	OutputUSDPerMTok *float64
	BlockedByKeyIDs  []int64
	updatedAt        time.Time
}

type MutationResult struct {
	Models        []Model
	PreviousKinds map[int64]Kind
}

type Candidate struct {
	PublicID                  string
	UpstreamID                string
	DisplayName               string
	Kind                      Kind
	Provider                  string
	Channel                   string
	Badge                     string
	Status                    string
	Enabled                   bool
	Capabilities              []string
	CapabilityTags            []string
	SupportsVision            bool
	SupportsTools             bool
	SupportsReasoning         bool
	ReasoningWireFormat       string
	ReasoningLevels           []string
	ReasoningMinBudget        int
	ReasoningMaxBudget        int
	ReasoningZeroAllowed      bool
	ReasoningDynamicAllowed   bool
}

type Selection struct {
	PublicID                string
	UpstreamID              string
	DisplayName             string
	Kind                    Kind
	Provider                string
	Enabled                 bool
	SupportsVision          bool
	SupportsTools           bool
	SupportsReasoning       bool
	ReasoningWireFormat     string
	ReasoningLevels         []string
	ReasoningMinBudget      int
	ReasoningMaxBudget      int
	ReasoningZeroAllowed    bool
	ReasoningDynamicAllowed bool
	CapabilityVerifiedAt    *time.Time
}

type Requirements struct {
	Kind      Kind
	Vision    bool
	Tools     bool
	Reasoning bool
}

// Patch is an allowlist of mutable model fields.
type Patch struct {
	DisplayName             *string   `json:"display_name,omitempty"`
	Kind                    *Kind     `json:"kind,omitempty"`
	Provider                *string   `json:"provider,omitempty"`
	Enabled                 *bool     `json:"enabled,omitempty"`
	SupportsVision          *bool     `json:"supports_vision,omitempty"`
	SupportsTools           *bool     `json:"supports_tools,omitempty"`
	SupportsReasoning       *bool     `json:"supports_reasoning,omitempty"`
	ReasoningWireFormat     *string   `json:"reasoning_wire_format,omitempty"`
	ReasoningLevels         *[]string `json:"reasoning_levels,omitempty"`
	ReasoningMinBudget      *int      `json:"reasoning_min_budget,omitempty"`
	ReasoningMaxBudget      *int      `json:"reasoning_max_budget,omitempty"`
	ReasoningZeroAllowed    *bool     `json:"reasoning_zero_allowed,omitempty"`
	ReasoningDynamicAllowed *bool     `json:"reasoning_dynamic_allowed,omitempty"`
	// StreamFirstTokenTimeoutMS / StreamIdleTimeoutMS override the global
	// runtime_settings windows for this model. They mirror the models-table
	// columns seeded by migration 016/022; a nil value leaves the current value.
	StreamFirstTokenTimeoutMS *int `json:"stream_first_token_timeout_ms,omitempty"`
	StreamIdleTimeoutMS       *int `json:"stream_idle_timeout_ms,omitempty"`
	// Pricing is kept separate from capability Selection (which models
	// upstream-discovered attributes): these are operator-owned cost columns.
	InputUSDPerMTok  *float64 `json:"input_usd_per_mtok,omitempty"`
	OutputUSDPerMTok *float64 `json:"output_usd_per_mtok,omitempty"`
}

func (m Model) ReasoningProfile() compat.ReasoningProfile {
	levels := make([]compat.ReasoningLevel, 0, len(m.ReasoningLevels))
	for _, level := range m.ReasoningLevels {
		levels = append(levels, compat.ReasoningLevel(level))
	}
	if len(levels) == 0 {
		levels = []compat.ReasoningLevel{
			compat.ReasoningNone, compat.ReasoningAuto, compat.ReasoningMinimal, compat.ReasoningLow,
			compat.ReasoningMedium, compat.ReasoningHigh, compat.ReasoningXHigh, compat.ReasoningMax,
		}
	}
	maxBudget := m.ReasoningMaxBudget
	if maxBudget == 0 {
		maxBudget = 128000
	}
	return compat.ReasoningProfile{
		Supported: m.SupportsReasoning, Levels: levels,
		MinBudget: m.ReasoningMinBudget, MaxBudget: maxBudget,
		ZeroAllowed:    m.ReasoningZeroAllowed || len(m.ReasoningLevels) == 0,
		DynamicAllowed: m.ReasoningDynamicAllowed || len(m.ReasoningLevels) == 0,
		WireFormat:     m.ReasoningWireFormat,
	}
}

func normalizeModelSelection(selection Selection) (Selection, error) {
	normalized, err := normalizeSelection(selection)
	if err == nil || errors.Is(err, ErrCapabilityUnverified) {
		return normalized, err
	}
	return Selection{}, fmt.Errorf("%w: %v", ErrInvalidModelSelection, err)
}

func validateEnabledProvider(provider string, enabled bool) error {
	if provider == "" {
		provider = defaultModelProvider
	}
	if provider != ProviderNVIDIA && provider != ProviderOpenCodeFree {
		return fmt.Errorf("%w: unsupported model provider %q", ErrInvalidModelSelection, provider)
	}
	if enabled && provider != ProviderNVIDIA {
		return fmt.Errorf("%w: only NVIDIA provider models can be enabled", ErrInvalidModelSelection)
	}
	return nil
}
