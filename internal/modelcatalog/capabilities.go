package modelcatalog

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrModelNotFound         = errors.New("model not found")
	ErrModelKindMismatch     = errors.New("model kind does not match endpoint")
	ErrCapabilityUnsupported = errors.New("model capability is not supported")
	ErrCapabilityUnverified  = errors.New("model capability is not verified")
	ErrManualTestRequired    = errors.New("successful manual test is required")
	ErrNVIDIAKeyRequired     = errors.New("an NVIDIA key is required")
	ErrProviderMismatch      = errors.New("model provider does not match the selected test channel")
	ErrProviderNotRoutable   = errors.New("model provider is not routable")
	ErrProviderNotConfigured = errors.New("model provider is not configured")
)

func normalizeSelection(selection Selection) (Selection, error) {
	if strings.TrimSpace(selection.PublicID) == "" || strings.TrimSpace(selection.UpstreamID) == "" || strings.TrimSpace(selection.DisplayName) == "" {
		return Selection{}, errors.New("model IDs and display name are required")
	}
	selection.Provider = strings.TrimSpace(selection.Provider)
	if selection.Provider == "" {
		selection.Provider = defaultModelProvider
	}
	if err := validateEnabledProvider(selection.Provider, selection.Enabled); err != nil {
		return Selection{}, err
	}
	if !validKind(selection.Kind) {
		return Selection{}, fmt.Errorf("unsupported model kind %q", selection.Kind)
	}
	if selection.ReasoningWireFormat == "" {
		selection.ReasoningWireFormat = "none"
	}
	if selection.SupportsReasoning != (selection.ReasoningWireFormat == "openai") {
		return Selection{}, errors.New("reasoning capability and wire format disagree")
	}
	if selection.Enabled && requiresVerification(selection.Kind) && selection.CapabilityVerifiedAt == nil {
		return Selection{}, ErrCapabilityUnverified
	}
	return selection, nil
}

func validateRequirements(model Model, requirements Requirements) error {
	if model.Kind != requirements.Kind {
		return ErrModelKindMismatch
	}
	if requirements.Vision && !model.SupportsVision || requirements.Tools && !model.SupportsTools || requirements.Reasoning && !model.SupportsReasoning {
		return ErrCapabilityUnsupported
	}
	if requiresVerification(model.Kind) && model.CapabilityVerifiedAt == nil {
		return ErrCapabilityUnverified
	}
	return nil
}

func validKind(kind Kind) bool {
	switch kind {
	case KindChat, KindEmbedding, KindASR, KindTTS:
		return true
	default:
		return false
	}
}

func requiresVerification(kind Kind) bool {
	return kind == KindASR || kind == KindTTS
}
