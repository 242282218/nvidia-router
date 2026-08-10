package nvidia

import (
	"nvidia-router/internal/provider"
)

// Compile-time assertion: the NVIDIA client is the single provider wired at
// startup. Keeping the interface satisfaction on the concrete type means v1
// handlers and tests keep passing *nvidia.Client while the request path only
// ever depends on provider.Provider.
var _ provider.Provider = (*Client)(nil)

// ID identifies the NVIDIA provider for logs and future multi-provider routing.
func (c *Client) ID() string { return "nvidia" }

// CapabilityHint converts the descriptor's provider-specific hint into the
// provider-agnostic mirror consumed across the abstraction boundary.
func (c *Client) CapabilityHint(modelID string) provider.CapabilityHint {
	hint := c.descriptor.CapabilityHint(modelID)
	return provider.CapabilityHint{
		Kind:                string(hint.Kind),
		SupportsVision:      hint.SupportsVision,
		SupportsTools:       hint.SupportsTools,
		SupportsReasoning:   hint.SupportsReasoning,
		ReasoningWireFormat: string(hint.ReasoningWireFormat),
	}
}
