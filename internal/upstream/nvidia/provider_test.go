package nvidia

import (
	"testing"

	"nvidia-router/internal/provider"
)

// TestClientImplementsProvider guards the R4.1 abstraction: the compile-time
// assertion in provider.go catches signature drift, this test pins the visible
// behaviour of the provider surface the request path relies on.
func TestClientImplementsProvider(t *testing.T) {
	var p provider.Provider = &Client{}
	if p.ID() != "nvidia" {
		t.Fatalf("provider ID = %q, want nvidia", p.ID())
	}
}

func TestClientCapabilityHintConvertsDescriptorHints(t *testing.T) {
	client := &Client{descriptor: DefaultDescriptor()}
	unknown := client.CapabilityHint("unknown/model")
	if unknown.Kind != "chat" || unknown.SupportsReasoning || unknown.ReasoningWireFormat != "none" {
		t.Fatalf("unknown-model hint = %#v, want chat/none with no reasoning", unknown)
	}
	reasoning := client.CapabilityHint("deepseek-ai/deepseek-v4-flash")
	if reasoning.Kind != "chat" || !reasoning.SupportsReasoning || reasoning.ReasoningWireFormat != "openai" {
		t.Fatalf("reasoning hint = %#v, want chat/openai with reasoning", reasoning)
	}
}
