package modelcatalog

import "time"

type Kind string

const (
	KindChat      Kind = "chat"
	KindEmbedding Kind = "embedding"
	KindASR       Kind = "asr"
	KindTTS       Kind = "tts"
)

type Model struct {
	ID                   int64
	PublicID             string
	UpstreamID           string
	DisplayName          string
	Kind                 Kind
	Enabled              bool
	SupportsVision       bool
	SupportsTools        bool
	SupportsReasoning    bool
	ReasoningWireFormat  string
	CapabilityVerifiedAt *time.Time
}

type Candidate struct {
	UpstreamID          string
	DisplayName         string
	Kind                Kind
	SupportsVision      bool
	SupportsTools       bool
	SupportsReasoning   bool
	ReasoningWireFormat string
}

type Selection struct {
	PublicID             string
	UpstreamID           string
	DisplayName          string
	Kind                 Kind
	Enabled              bool
	SupportsVision       bool
	SupportsTools        bool
	SupportsReasoning    bool
	ReasoningWireFormat  string
	CapabilityVerifiedAt *time.Time
}

type Requirements struct {
	Kind      Kind
	Vision    bool
	Tools     bool
	Reasoning bool
}

// Patch is an allowlist of mutable model fields.
type Patch struct {
	DisplayName         *string `json:"display_name,omitempty"`
	Kind                *Kind   `json:"kind,omitempty"`
	Enabled             *bool   `json:"enabled,omitempty"`
	SupportsVision      *bool   `json:"supports_vision,omitempty"`
	SupportsTools       *bool   `json:"supports_tools,omitempty"`
	SupportsReasoning   *bool   `json:"supports_reasoning,omitempty"`
	ReasoningWireFormat *string `json:"reasoning_wire_format,omitempty"`
}
