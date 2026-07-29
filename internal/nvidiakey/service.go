package nvidiakey

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/upstream/nvidia"
)

const nvidiaKeyAAD = "nvidia-key:v1"

type Service struct {
	repository *Repository
	keys       *crypto.KeySet
	validator  CredentialValidator
	clock      clock.Clock
	random     fault.RandomSource
}

type KeyStateWriter interface {
	MarkSuccess(ctx context.Context, keyID int64) (keystate.KeySnapshot, error)
	MarkFailure(ctx context.Context, keyID, modelID int64, f fault.Fault) (keystate.KeySnapshot, error)
}

func NewService(repository *Repository, keys *crypto.KeySet, validator CredentialValidator, source clock.Clock) *Service {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Service{
		repository: repository,
		keys:       keys,
		validator:  validator,
		clock:      source,
		random:     fault.RandomFunc(rand.Float64),
	}
}

func (s *Service) MarkSuccess(ctx context.Context, keyID int64) (keystate.KeySnapshot, error) {
	snapshot, err := s.repository.markSuccess(ctx, keyID, s.clock.Now())
	if err != nil {
		return keystate.KeySnapshot{}, fmt.Errorf("mark NVIDIA key success: %w", err)
	}
	return snapshot, nil
}

func (s *Service) MarkFailure(ctx context.Context, keyID, modelID int64, f fault.Fault) (keystate.KeySnapshot, error) {
	snapshot, err := s.repository.markFailure(ctx, keyID, modelID, f, s.clock.Now(), s.random)
	if err != nil {
		return keystate.KeySnapshot{}, fmt.Errorf("mark NVIDIA key failure: %w", err)
	}
	return snapshot, nil
}

func (s *Service) Import(ctx context.Context, token string) (ImportResult, error) {
	if !validToken(token) {
		return ImportResult{Status: ImportStatusInvalid, Reason: "invalid_format", Masked: "invalid"}, nil
	}
	masked, prefix, suffix := maskToken(token)
	fingerprintInput := []byte(token)
	fingerprint := s.keys.Fingerprint(fingerprintInput)
	crypto.Zero(fingerprintInput)
	defer crypto.Zero(fingerprint)

	exists, err := s.repository.FingerprintExists(ctx, fingerprint)
	if err != nil {
		return ImportResult{}, fmt.Errorf("check duplicate NVIDIA key: %w", err)
	}
	if exists {
		return ImportResult{Status: ImportStatusDuplicate, Reason: "duplicate", Masked: masked}, nil
	}

	validation := s.validator.ValidateCredential(ctx, token)
	if validation.State != nvidia.ValidationValid {
		status, reason := validationStatus(validation.State)
		return ImportResult{Status: status, Reason: reason, Masked: masked}, nil
	}

	plaintext := []byte(token)
	ciphertext, nonce, err := s.keys.Encrypt(plaintext, nvidiaKeyAAD)
	crypto.Zero(plaintext)
	if err != nil {
		return ImportResult{}, fmt.Errorf("encrypt NVIDIA key: %w", err)
	}
	defer crypto.Zero(ciphertext)
	defer crypto.Zero(nonce)

	key, duplicate, err := s.repository.Create(ctx, ciphertext, nonce, fingerprint, prefix, suffix, s.clock.Now())
	if err != nil {
		return ImportResult{}, fmt.Errorf("persist NVIDIA key: %w", err)
	}
	if duplicate {
		return ImportResult{Status: ImportStatusDuplicate, Reason: "duplicate", Masked: masked}, nil
	}
	return ImportResult{Status: ImportStatusImported, Reason: "valid", Masked: masked, Key: &key}, nil
}

func (s *Service) ImportBatch(ctx context.Context, input string) []ImportResult {
	lines := strings.Split(input, "\n")
	results := make([]ImportResult, 0, len(lines))
	for index, line := range lines {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		result, err := s.Import(ctx, token)
		if err != nil {
			slog.Error("import NVIDIA key", "error_type", fmt.Sprintf("%T", err))
			result = ImportResult{Status: ImportStatusIndeterminate, Reason: "internal_error", Masked: safeMask(token)}
		}
		result.Line = index + 1
		results = append(results, result)
	}
	return results
}

func (s *Service) WithSecret(ctx context.Context, id int64, callback func([]byte) error) error {
	encrypted, err := s.repository.LoadEncrypted(ctx, id)
	if err != nil {
		return fmt.Errorf("load NVIDIA key secret: %w", err)
	}
	secret, err := s.keys.Decrypt(encrypted.ciphertext, encrypted.nonce, nvidiaKeyAAD)
	if err != nil {
		return fmt.Errorf("decrypt NVIDIA key secret: %w", err)
	}
	defer crypto.Zero(secret)
	if err := callback(secret); err != nil {
		return fmt.Errorf("use NVIDIA key secret: %w", err)
	}
	return nil
}

func safeMask(token string) string {
	if !validToken(token) {
		return "invalid"
	}
	masked, _, _ := maskToken(token)
	return masked
}
