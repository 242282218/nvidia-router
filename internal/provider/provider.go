// Package provider defines the upstream LLM surface the request path drives.
// The process wires exactly one provider today (NVIDIA); the interface exists so
// a second upstream can plug in later without touching the HTTP handlers or the
// attempt loop. Multi-provider routing is deliberately out of scope: callers
// receive the single provider constructed at startup.
package provider

import (
	"context"
	"net/http"

	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
)

// CapabilityHint mirrors the subset of upstream model capabilities the router
// needs before the model catalog is consulted. It stays a plain value mirror so
// provider-specific types never leak across the abstraction boundary.
type CapabilityHint struct {
	Kind                string
	SupportsVision      bool
	SupportsTools       bool
	SupportsReasoning   bool
	ReasoningWireFormat string
}

// Provider abstracts every upstream call the request path makes. Token handling
// matches the current single-provider wiring: credentials arrive as opaque
// strings, and per-attempt timeouts come from a runtimeconfig.Snapshot.
type Provider interface {
	// ID returns a stable provider identifier for logs and future routing.
	ID() string
	// Models lists the upstream model IDs visible to a credential.
	Models(ctx context.Context, token string) ([]string, error)
	// Chat issues a chat-completions request, streaming or not. The response
	// body stays open for the caller to consume or validate.
	Chat(ctx context.Context, snapshot runtimeconfig.Snapshot, token string, body []byte, stream bool) (*http.Response, error)
	// Embeddings issues a non-streaming embeddings request.
	Embeddings(ctx context.Context, snapshot runtimeconfig.Snapshot, token string, body []byte) (*http.Response, error)
	// AudioTranscriptionsReplay issues an ASR request from a replayable body so
	// the attempt loop can retry the same upload against another credential.
	AudioTranscriptionsReplay(ctx context.Context, snapshot runtimeconfig.Snapshot, token string, body router.ReplayableBody, contentType string) (*http.Response, error)
	// AudioSpeech issues a streaming TTS request.
	AudioSpeech(ctx context.Context, snapshot runtimeconfig.Snapshot, token string, body []byte) (*http.Response, error)
	// CapabilityHint reports the capabilities assumed for a model ID before the
	// catalog has verified them.
	CapabilityHint(modelID string) CapabilityHint
}
