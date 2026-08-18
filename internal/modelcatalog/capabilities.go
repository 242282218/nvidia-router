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
)

func normalizeSelection(selection Selection) (Selection, error) {
	if strings.TrimSpace(selection.PublicID) == "" || strings.TrimSpace(selection.UpstreamID) == "" || strings.TrimSpace(selection.DisplayName) == "" {
		return Selection{}, errors.New("model IDs and display name are required")
	}
	if !validKind(selection.Kind) {
		return Selection{}, fmt.Errorf("unsupported model kind %q", selection.Kind)
	}
	if err := normalizeReasoningProfile(&selection); err != nil {
		return Selection{}, err
	}
	if selection.Enabled && requiresVerification(selection.Kind) && selection.CapabilityVerifiedAt == nil {
		return Selection{}, ErrCapabilityUnverified
	}
	return selection, nil
}

func normalizeReasoningProfile(selection *Selection) error {
	if selection.ReasoningWireFormat == "" {
		if selection.SupportsReasoning {
			selection.ReasoningWireFormat = "openai"
		} else {
			selection.ReasoningWireFormat = "none"
		}
	}
	if selection.ReasoningWireFormat == "chain_of_thought" {
		selection.ReasoningWireFormat = "thinking"
	}
	switch selection.ReasoningWireFormat {
	case "none":
		if selection.SupportsReasoning {
			return errors.New("reasoning capability and wire format disagree")
		}
	case "openai", "thinking":
		if !selection.SupportsReasoning {
			return errors.New("reasoning wire format requires reasoning support")
		}
	default:
		return fmt.Errorf("unsupported reasoning wire format %q", selection.ReasoningWireFormat)
	}
	if selection.ReasoningMinBudget < 0 || selection.ReasoningMaxBudget < 0 {
		return errors.New("reasoning budgets must be non-negative")
	}
	if selection.ReasoningMaxBudget == 0 {
		selection.ReasoningMaxBudget = 128000
	}
	if selection.ReasoningMinBudget > selection.ReasoningMaxBudget {
		return errors.New("reasoning minimum budget exceeds maximum budget")
	}
	hasProfile := len(selection.ReasoningLevels) > 0 || selection.ReasoningMinBudget > 0 || selection.ReasoningMaxBudget != 128000 || selection.ReasoningZeroAllowed || selection.ReasoningDynamicAllowed
	if selection.SupportsReasoning && !hasProfile {
		selection.ReasoningLevels = defaultReasoningLevels()
		selection.ReasoningZeroAllowed = true
		selection.ReasoningDynamicAllowed = true
	}
	if !selection.SupportsReasoning && len(selection.ReasoningLevels) == 0 {
		selection.ReasoningLevels = []string{"none"}
	}
	for _, level := range selection.ReasoningLevels {
		if !validReasoningLevel(level) {
			return fmt.Errorf("unsupported reasoning level %q", level)
		}
	}
	return nil
}

func defaultReasoningLevels() []string {
	return []string{"none", "auto", "minimal", "low", "medium", "high", "xhigh", "max"}
}

func validReasoningLevel(level string) bool {
	switch level {
	case "none", "auto", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
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
