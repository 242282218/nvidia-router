package modelcatalog

import "time"

const (
	ProbeStatusSuccess     = "success"
	ProbeStatusFailed      = "failed"
	ProbeStatusVisible     = "visible"
	ProbeStatusHidden      = "hidden"
	ProbeStatusUnsupported = "unsupported"
	ProbeStatusUnknown     = "unknown"
)

// ProbeSummary is a compact, non-sensitive result for the admin test job. It
// never contains request/response bodies or reasoning text.
type ProbeSummary struct {
	Base                string `json:"base"`
	Reasoning           string `json:"reasoning"`
	ReasoningWireFormat string `json:"reasoning_wire_format,omitempty"`
	Tools               string `json:"tools"`
}

type probeCapabilityUpdate struct {
	SupportsReasoning   *bool
	ReasoningStatus     *string
	ReasoningWireFormat *string
	ToolsStatus         *string
	ToolsVerifiedAt     *time.Time
}
