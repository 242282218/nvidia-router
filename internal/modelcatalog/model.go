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
