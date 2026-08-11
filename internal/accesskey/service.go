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

var (
	ErrInvalidAccessKey  = errors.New("invalid access key")
	ErrAccessKeyNotFound = errors.New("access key not found")
)

type Service struct {
	repository *Repository
	keys       *crypto.KeySet
	clock      clock.Clock
	cache      *cache
	limiter    *limiter

	usageMu      sync.Mutex
	lastRecorded map[int64]time.Time
	pending      map[int64]struct{}

	// consumedWriteMu guards the throttled budget persistence: consumed tokens
	// are only written back at most once per minimum interval, mirroring the
	// last-used write path so the hot request path never hits the DB.
	consumedWriteMu sync.Mutex
	lastConsumed    map[int64]time.Time
	pendingConsumed map[int64]struct{}
}

func NewService(repository *Repository, keys *crypto.KeySet, source clock.Clock) *Service {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Service{
		repository:      repository,
		keys:            keys,
		clock:           source,
		cache:           newCache(defaultCacheTTL, defaultCacheEntries),
		limiter:         newLimiter(),
		lastRecorded:    make(map[int64]time.Time),
		pending:         make(map[int64]struct{}),
		lastConsumed:    make(map[int64]time.Time),
		pendingConsumed: make(map[int64]struct{}),
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

	created, err := s.repository.Create(ctx, name, digest, plaintext[:displayPrefixLength], s.clock.Now(), s.keys.ActiveVersion())
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
	digests := s.keys.AccessKeyDigests(digestInput)
	crypto.Zero(digestInput)
	for _, digest := range digests {
		defer crypto.Zero(digest)
	}

	now := s.clock.Now()
	activeVersion := s.keys.ActiveVersion()
	activeDigest := digests[activeVersion]
	if identity, ok := s.cache.lookup(activeDigest, now); ok {
		if identity.ExpiresAt != nil && !now.Before(*identity.ExpiresAt) {
			s.cache.invalidate()
			return AccessKeyIdentity{}, ErrInvalidAccessKey
		}
		return identity, nil
	}
	identity, matchedVersion, err := s.repository.Authenticate(ctx, digests)
	if err != nil {
		return AccessKeyIdentity{}, fmt.Errorf("authenticate access key: %w", err)
	}
	if identity.ExpiresAt != nil && !now.Before(*identity.ExpiresAt) {
		return AccessKeyIdentity{}, ErrInvalidAccessKey
	}
	if matchedVersion != activeVersion && activeDigest != nil {
		if err := s.repository.UpdateDigest(ctx, identity.ID, activeDigest, activeVersion); err != nil {
			slog.Warn("migrate access key digest", "error_type", fmt.Sprintf("%T", err))
		}
	}
	s.cache.store(activeDigest, identity, now)
	return identity, nil
}

func (s *Service) BeginRequest(identity AccessKeyIdentity) error {
	persisted := identity.ConsumedTokens
	return s.limiter.begin(identity.ID, identity.RPMLimit, identity.TPMLimit, identity.MaxConcurrent, identity.TokenBudget, persisted, s.clock.Now())
}

func (s *Service) ChargeUsage(identity AccessKeyIdentity, prompt, completion *int64) {
	if prompt == nil && completion == nil {
		return
	}
	s.limiter.charge(identity.ID, identity.TPMLimit, valueOrZero(prompt), valueOrZero(completion), s.clock.Now())
	// Persist the budget counter so a restart does not lose the spend. Throttled
	// to at most one write per key per minute, exactly like last-used updates.
	if identity.TokenBudget > 0 {
		s.persistConsumed(identity.ID)
	}
}

func (s *Service) EndRequest(identity AccessKeyIdentity) {
	s.limiter.release(identity.ID)
}

func (s *Service) UpdatePolicy(ctx context.Context, id int64, expiresAt *time.Time, rpm, tpm, maxConcurrent int, tokenBudget *int64) error {
	if id <= 0 {
		return fmt.Errorf("update access key policy: invalid id")
	}
	if rpm < 0 || rpm > 100000 || tpm < 0 || tpm > 1000000000 || maxConcurrent < 0 || maxConcurrent > 10000 {
		return fmt.Errorf("update access key policy: limit is out of range")
	}
	if tokenBudget != nil && (*tokenBudget < 0 || *tokenBudget > 1000000000000) {
		return fmt.Errorf("update access key policy: token budget is out of range")
	}
	if expiresAt != nil {
		expiry := expiresAt.UTC()
		if !expiry.After(s.clock.Now().UTC()) {
			return fmt.Errorf("update access key policy: expiration must be in the future")
		}
		expiresAt = &expiry
	}
	if err := s.repository.UpdatePolicy(ctx, id, expiresAt, rpm, tpm, maxConcurrent, tokenBudget); err != nil {
		return fmt.Errorf("update access key policy: %w", err)
	}
	// Policy changes must not wait for the authentication cache TTL. The budget
	// cap itself is read fresh on every begin via the identity, so dropping the
	// auth cache is enough to pick up a raised/lowered cap immediately.
	s.cache.invalidate()
	return nil
}

// persistConsumed writes the in-memory budget counter to the database at most
// once per minute per key. It mirrors recordUse's throttling so a high-volume
// key does not hammer the single SQLite writer on every charged request.
func (s *Service) persistConsumed(id int64) {
	now := s.clock.Now().UTC().Truncate(time.Second)
	s.consumedWriteMu.Lock()
	last, recorded := s.lastConsumed[id]
	_, pending := s.pendingConsumed[id]
	if pending || recorded && now.Sub(last) < lastUsedWriteMinimum {
		s.consumedWriteMu.Unlock()
		return
	}
	s.pendingConsumed[id] = struct{}{}
	s.consumedWriteMu.Unlock()

	go func() {
		consumed := s.limiter.consumedTotal(id)
		err := s.repository.UpdateConsumedTokens(context.Background(), id, consumed)
		if err != nil {
			slog.Error("update access key consumed tokens", "error_type", fmt.Sprintf("%T", err))
		}
		s.consumedWriteMu.Lock()
		delete(s.pendingConsumed, id)
		if err == nil {
			s.lastConsumed[id] = now
		}
		s.consumedWriteMu.Unlock()
	}()
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func (s *Service) Revoke(ctx context.Context, id int64) error {
	if err := s.repository.Revoke(ctx, id, s.clock.Now()); err != nil {
		return fmt.Errorf("revoke access key: %w", err)
	}
	// Revocation must take effect immediately rather than after the cache TTL.
	// Entries are keyed by digest and the caller only has the ID, so drop all of
	// them; revocation is a rare admin action and the cache refills on demand.
	s.cache.invalidate()
	// Drop usage-tracking entries so revoked keys do not accumulate forever.
	s.usageMu.Lock()
	delete(s.lastRecorded, id)
	delete(s.pending, id)
	s.usageMu.Unlock()
	// Also drop the throttled budget-write tracker for the same reason.
	s.consumedWriteMu.Lock()
	delete(s.lastConsumed, id)
	delete(s.pendingConsumed, id)
	s.consumedWriteMu.Unlock()
	// Drop the rate-limit bucket so a revoked key's state does not accumulate in
	// the limiter map for the process lifetime (in-flight requests already
	// drained; a re-created key seeds a fresh bucket on its first use).
	s.limiter.remove(id)
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
