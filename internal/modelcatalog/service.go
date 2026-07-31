package modelcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
	"nvidia-router/internal/xkproxy"
)

type SecretProvider interface {
	WithSecret(context.Context, int64, func([]byte) error) error
}

type ModelDiscoverer interface {
	Models(context.Context, string) ([]string, error)
}

type chatModelTester interface {
	Chat(context.Context, runtimeconfig.Snapshot, string, []byte, bool) (*http.Response, error)
}

type embeddingModelTester interface {
	Embeddings(context.Context, runtimeconfig.Snapshot, string, []byte) (*http.Response, error)
}

type asrModelTester interface {
	AudioTranscriptions(context.Context, runtimeconfig.Snapshot, string, []byte, string) (*http.Response, error)
}

type ttsModelTester interface {
	AudioSpeech(context.Context, runtimeconfig.Snapshot, string, []byte) (*http.Response, error)
}

const modelVerificationTimeout = 30 * time.Second

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
	_, err := s.SaveSelectionResult(ctx, selections)
	return err
}

func (s *Service) SaveSelectionResult(ctx context.Context, selections []Selection) (MutationResult, error) {
	normalized := make([]Selection, len(selections))
	for index, selection := range selections {
		value, err := normalizeModelSelection(selection)
		if err != nil {
			return MutationResult{}, fmt.Errorf("validate selected model %q: %w", selection.PublicID, err)
		}
		normalized[index] = value
	}
	result, err := s.repository.SaveSelectionsResult(ctx, normalized, s.clock.Now())
	if err != nil {
		return MutationResult{}, fmt.Errorf("save model selection: %w", err)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context) ([]Model, error) {
	models, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	return models, nil
}

func (s *Service) Patch(ctx context.Context, id int64, patch Patch) (Model, error) {
	updated, _, err := s.PatchResult(ctx, id, patch)
	return updated, err
}

func (s *Service) PatchResult(ctx context.Context, id int64, patch Patch) (Model, Kind, error) {
	model, previousKind, err := s.repository.Patch(ctx, id, patch, s.clock.Now())
	if err != nil {
		return Model{}, "", fmt.Errorf("save model patch: %w", err)
	}
	return model, previousKind, nil
}

func (s *Service) VerifyAndUnblock(ctx context.Context, keyID, modelID int64) (Model, error) {
	model, err := s.repository.Get(ctx, modelID)
	if err != nil {
		return Model{}, fmt.Errorf("load model before manual test: %w", err)
	}
	verifyCtx, cancel := context.WithTimeout(ctx, modelVerificationTimeout)
	defer cancel()
	if err := s.testTargetModel(verifyCtx, keyID, model); err != nil {
		return Model{}, fmt.Errorf("manual model test: %w", err)
	}
	verified, err := s.repository.VerifyAndUnblock(ctx, keyID, modelID, model.updatedAt, s.clock.Now())
	if err != nil {
		return Model{}, fmt.Errorf("verify model and clear block: %w", err)
	}
	return verified, nil
}

func (s *Service) testTargetModel(ctx context.Context, keyID int64, model Model) error {
	return s.secrets.WithSecret(ctx, keyID, func(secret []byte) error {
		var response *http.Response
		var err error
		snapshot := runtimeconfig.Snapshot{}
		switch model.Kind {
		case KindChat:
			tester, ok := s.discoverer.(chatModelTester)
			if !ok {
				return ErrManualTestRequired
			}
			body, marshalErr := json.Marshal(map[string]any{"model": model.UpstreamID, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 1})
			if marshalErr != nil {
				return marshalErr
			}
			response, err = tester.Chat(ctx, snapshot, string(secret), body, false)
		case KindEmbedding:
			tester, ok := s.discoverer.(embeddingModelTester)
			if !ok {
				return ErrManualTestRequired
			}
			body, marshalErr := json.Marshal(map[string]any{"model": model.UpstreamID, "input": "ping"})
			if marshalErr != nil {
				return marshalErr
			}
			response, err = tester.Embeddings(ctx, snapshot, string(secret), body)
		case KindASR:
			tester, ok := s.discoverer.(asrModelTester)
			if !ok {
				return ErrManualTestRequired
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			if err := writer.WriteField("model", model.UpstreamID); err != nil {
				return err
			}
			part, partErr := writer.CreateFormFile("file", "probe.wav")
			if partErr != nil {
				return partErr
			}
			if _, err := part.Write(probeWAV); err != nil {
				return err
			}
			if err = writer.Close(); err != nil {
				return err
			}
			response, err = tester.AudioTranscriptions(ctx, snapshot, string(secret), body.Bytes(), writer.FormDataContentType())
		case KindTTS:
			tester, ok := s.discoverer.(ttsModelTester)
			if !ok {
				return ErrManualTestRequired
			}
			body, marshalErr := json.Marshal(map[string]any{"model": model.UpstreamID, "input": "ping", "voice": "Magpie-Multilingual.EN-US.Aria", "response_format": "wav"})
			if marshalErr != nil {
				return marshalErr
			}
			response, err = tester.AudioSpeech(ctx, snapshot, string(secret), body)
		default:
			return ErrManualTestRequired
		}
		if err != nil {
			var proxyErr *xkproxy.Error
			if errors.As(err, &proxyErr) {
				return err
			}
			return ErrManualTestRequired
		}
		if response == nil || response.Body == nil {
			return ErrManualTestRequired
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return ErrManualTestRequired
		}
		switch model.Kind {
		case KindChat:
			_, err = nvidia.ValidateNonstreamChat(response)
		case KindEmbedding:
			_, err = nvidia.ValidateNonstreamEmbeddings(response)
		case KindASR:
			_, err = nvidia.ValidateNonstreamAudio(response)
		case KindTTS:
			if !isAudioContentType(response.Header.Get("Content-Type")) {
				return ErrManualTestRequired
			}
			err = nvidia.PrimeAudioSpeech(ctx, response)
		}
		if err != nil {
			return ErrManualTestRequired
		}
		return nil
	})
}

// probeWAV is a minimal valid mono PCM WAV containing one silent sample. It is
// deliberately generated in memory so verification never reads a caller file.
func isAudioContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return strings.HasPrefix(mediaType, "audio/") || mediaType == "application/octet-stream"
}

var probeWAV = []byte{
	'R', 'I', 'F', 'F', 0x26, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E',
	'f', 'm', 't', ' ', 0x10, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00,
	0x40, 0x1f, 0x00, 0x00, 0x80, 0x3e, 0x00, 0x00, 0x02, 0x00, 0x10, 0x00,
	'd', 'a', 't', 'a', 0x02, 0x00, 0x00, 0x00, 0x00, 0x00,
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
	if err := s.repository.SetEnabled(ctx, id, enabled, s.clock.Now()); err != nil {
		return fmt.Errorf("set model enabled state: %w", err)
	}
	return nil
}

func (s *Service) SetCapabilityVerified(ctx context.Context, id int64, verifiedAt *time.Time) error {
	if err := s.repository.SetCapabilityVerified(ctx, id, verifiedAt, s.clock.Now()); err != nil {
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
