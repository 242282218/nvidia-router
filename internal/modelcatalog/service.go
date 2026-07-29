package modelcatalog

import (
	"context"
	"fmt"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/upstream/nvidia"
)

type SecretProvider interface {
	WithSecret(context.Context, int64, func([]byte) error) error
}

type ModelDiscoverer interface {
	Models(context.Context, string) ([]string, error)
}

type Service struct {
	repository *Repository
	secrets    SecretProvider
	discoverer ModelDiscoverer
	descriptor nvidia.Descriptor
	clock      clock.Clock
}

func NewService(repository *Repository, secrets SecretProvider, discoverer ModelDiscoverer, descriptor nvidia.Descriptor, source clock.Clock) *Service {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Service{repository: repository, secrets: secrets, discoverer: discoverer, descriptor: descriptor, clock: source}
}

func (s *Service) DiscoverCandidates(ctx context.Context, keyID int64) ([]Candidate, error) {
	var modelIDs []string
	if err := s.secrets.WithSecret(ctx, keyID, func(secret []byte) error {
		models, err := s.discoverer.Models(ctx, string(secret))
		if err != nil {
			return fmt.Errorf("discover NVIDIA models: %w", err)
		}
		modelIDs = models
		return nil
	}); err != nil {
		return nil, fmt.Errorf("discover model candidates: %w", err)
	}
	candidates := make([]Candidate, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		hint := s.descriptor.CapabilityHint(modelID)
		candidates = append(candidates, candidateFromHint(modelID, hint))
	}
	return candidates, nil
}

func (s *Service) SaveSelection(ctx context.Context, selections []Selection) error {
	normalized := make([]Selection, len(selections))
	for index, selection := range selections {
		value, err := normalizeSelection(selection)
		if err != nil {
			return fmt.Errorf("validate selected model %q: %w", selection.PublicID, err)
		}
		normalized[index] = value
	}
	if err := s.repository.SaveSelections(ctx, normalized, s.clock.Now()); err != nil {
		return fmt.Errorf("save model selection: %w", err)
	}
	return nil
}

func (s *Service) ListEnabled(ctx context.Context) ([]Model, error) {
	models, err := s.repository.ListEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled models: %w", err)
	}
	return models, nil
}

func (s *Service) Resolve(ctx context.Context, publicID string, requirements Requirements) (Model, error) {
	model, err := s.repository.ResolveEnabled(ctx, publicID)
	if err != nil {
		return Model{}, fmt.Errorf("resolve model %q: %w", publicID, err)
	}
	if err := validateRequirements(model, requirements); err != nil {
		return Model{}, fmt.Errorf("validate model %q capabilities: %w", publicID, err)
	}
	return model, nil
}

func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if enabled {
		model, err := s.repository.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("load model before enabling: %w", err)
		}
		if requiresVerification(model.Kind) && model.CapabilityVerifiedAt == nil {
			return ErrCapabilityUnverified
		}
	}
	if err := s.repository.SetEnabled(ctx, id, enabled, s.clock.Now()); err != nil {
		return fmt.Errorf("set model enabled state: %w", err)
	}
	return nil
}

func (s *Service) SetCapabilityVerified(ctx context.Context, id int64, verifiedAt *time.Time) error {
	model, err := s.repository.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("load model before setting capability verification: %w", err)
	}
	disable := verifiedAt == nil && requiresVerification(model.Kind)
	if err := s.repository.SetCapabilityVerified(ctx, id, verifiedAt, disable, s.clock.Now()); err != nil {
		return fmt.Errorf("set model capability verification: %w", err)
	}
	return nil
}

func (s *Service) BlockKeyModel(ctx context.Context, keyID, modelID int64, reason string, upstreamStatus *int) error {
	if err := s.repository.BlockKeyModel(ctx, keyID, modelID, reason, upstreamStatus, s.clock.Now()); err != nil {
		return fmt.Errorf("block NVIDIA key for model: %w", err)
	}
	return nil
}

func (s *Service) UnblockKeyModel(ctx context.Context, keyID, modelID int64, manualTestSucceeded bool) error {
	if !manualTestSucceeded {
		return ErrManualTestRequired
	}
	if err := s.repository.UnblockKeyModel(ctx, keyID, modelID); err != nil {
		return fmt.Errorf("unblock NVIDIA key for model: %w", err)
	}
	return nil
}

func (s *Service) DeleteModel(ctx context.Context, id int64) error {
	if err := s.repository.DeleteModel(ctx, id); err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	return nil
}

func candidateFromHint(modelID string, hint nvidia.CapabilityHint) Candidate {
	return Candidate{
		UpstreamID:          modelID,
		DisplayName:         modelID,
		Kind:                Kind(hint.Kind),
		SupportsVision:      hint.SupportsVision,
		SupportsTools:       hint.SupportsTools,
		SupportsReasoning:   hint.SupportsReasoning,
		ReasoningWireFormat: string(hint.ReasoningWireFormat),
	}
}
