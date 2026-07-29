package accesskey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/crypto"
)

const (
	accessKeyPrefix      = "nvr_"
	accessKeyBytes       = 32
	displayPrefixLength  = 12
	lastUsedWriteMinimum = time.Minute
)

var ErrInvalidAccessKey = errors.New("invalid access key")

type Service struct {
	repository *Repository
	keys       *crypto.KeySet
	clock      clock.Clock

	usageMu      sync.Mutex
	lastRecorded map[int64]time.Time
	pending      map[int64]struct{}
}

func NewService(repository *Repository, keys *crypto.KeySet, source clock.Clock) *Service {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Service{
		repository:   repository,
		keys:         keys,
		clock:        source,
		lastRecorded: make(map[int64]time.Time),
		pending:      make(map[int64]struct{}),
	}
}

func (s *Service) Create(ctx context.Context, name string) (CreatedKey, error) {
	random := make([]byte, accessKeyBytes)
	if _, err := rand.Read(random); err != nil {
		return CreatedKey{}, fmt.Errorf("generate access key: %w", err)
	}
	defer crypto.Zero(random)

	plaintext := accessKeyPrefix + base64.RawURLEncoding.EncodeToString(random)
	digestInput := []byte(plaintext)
	digest := s.keys.AccessKeyDigest(digestInput)
	crypto.Zero(digestInput)
	defer crypto.Zero(digest)

	created, err := s.repository.Create(ctx, name, digest, plaintext[:displayPrefixLength], s.clock.Now())
	if err != nil {
		return CreatedKey{}, fmt.Errorf("create access key: %w", err)
	}
	return CreatedKey{Key: created, Plaintext: plaintext}, nil
}

func (s *Service) List(ctx context.Context) ([]Key, error) {
	keys, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list access keys: %w", err)
	}
	return keys, nil
}

func (s *Service) Authenticate(ctx context.Context, plaintext string) (AccessKeyIdentity, error) {
	if !validAccessKey(plaintext) {
		return AccessKeyIdentity{}, ErrInvalidAccessKey
	}
	digestInput := []byte(plaintext)
	digest := s.keys.AccessKeyDigest(digestInput)
	crypto.Zero(digestInput)
	defer crypto.Zero(digest)

	identity, err := s.repository.Authenticate(ctx, digest)
	if err != nil {
		return AccessKeyIdentity{}, fmt.Errorf("authenticate access key: %w", err)
	}
	return identity, nil
}

func (s *Service) Revoke(ctx context.Context, id int64) error {
	if err := s.repository.Revoke(ctx, id, s.clock.Now()); err != nil {
		return fmt.Errorf("revoke access key: %w", err)
	}
	return nil
}

func (s *Service) RecordUse(ctx context.Context, id int64) {
	now := s.clock.Now().UTC().Truncate(time.Second)
	s.usageMu.Lock()
	last, recorded := s.lastRecorded[id]
	_, pending := s.pending[id]
	if pending || recorded && now.Sub(last) < lastUsedWriteMinimum {
		s.usageMu.Unlock()
		return
	}
	s.pending[id] = struct{}{}
	s.usageMu.Unlock()

	go s.recordUse(context.WithoutCancel(ctx), id, now)
}

func (s *Service) recordUse(ctx context.Context, id int64, usedAt time.Time) {
	err := s.repository.UpdateLastUsed(ctx, id, usedAt, lastUsedWriteMinimum)
	if err != nil {
		slog.Error("update access key last-used time", "error_type", fmt.Sprintf("%T", err))
	}
	s.usageMu.Lock()
	delete(s.pending, id)
	if err == nil {
		s.lastRecorded[id] = usedAt
	}
	s.usageMu.Unlock()
}

func validAccessKey(value string) bool {
	if len(value) != len(accessKeyPrefix)+base64.RawURLEncoding.EncodedLen(accessKeyBytes) || !strings.HasPrefix(value, accessKeyPrefix) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(value, accessKeyPrefix))
	defer crypto.Zero(decoded)
	return err == nil && len(decoded) == accessKeyBytes
}
