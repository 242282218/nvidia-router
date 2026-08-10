package crypto

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
)

const (
	aeadKeyInfo        = "nvidia-router/aead/v1"
	fingerprintKeyInfo = "nvidia-router/fingerprint/v1"
	accessKeyInfo      = "nvidia-router/access-key/v1"
	sessionKeyInfo     = "nvidia-router/session/v1"
	derivedKeyLength   = 32
)

type KeySet struct {
	activeVersion int
	versions      map[int]derivedKeys

	// Keep active derived keys as fields for package-local zeroization tests and
	// compatibility with the original single-key representation.
	aeadKey        [derivedKeyLength]byte
	fingerprintKey [derivedKeyLength]byte
	accessKeyKey   [derivedKeyLength]byte
	sessionKey     [derivedKeyLength]byte
}

type derivedKeys struct {
	aeadKey        [derivedKeyLength]byte
	fingerprintKey [derivedKeyLength]byte
	accessKeyKey   [derivedKeyLength]byte
	sessionKey     [derivedKeyLength]byte
}

// New creates a single-version key set and preserves the legacy API.
func New(master [32]byte) (*KeySet, error) {
	return NewVersioned(1, master)
}

// NewVersioned creates a key set whose writes use activeVersion. Additional
// versions can be added with WithLegacyMasterKey for a bounded rotation window.
func NewVersioned(activeVersion int, master [32]byte) (*KeySet, error) {
	if activeVersion <= 0 {
		return nil, fmt.Errorf("active key version must be positive")
	}
	derived, err := deriveKeys(master)
	if err != nil {
		return nil, err
	}
	return newKeySet(activeVersion, map[int]derivedKeys{activeVersion: derived}), nil
}

// WithLegacyMasterKey adds a read-only legacy key version to the key set.
func (keys *KeySet) WithLegacyMasterKey(version int, master [32]byte) (*KeySet, error) {
	if keys == nil {
		return nil, fmt.Errorf("key set is nil")
	}
	if version <= 0 || version == keys.activeVersion {
		return nil, fmt.Errorf("legacy key version must be positive and differ from active version")
	}
	derived, err := deriveKeys(master)
	if err != nil {
		return nil, err
	}
	versions := make(map[int]derivedKeys, len(keys.versions)+1)
	for itemVersion, item := range keys.versions {
		versions[itemVersion] = item
	}
	versions[version] = derived
	return newKeySet(keys.activeVersion, versions), nil
}

func (keys *KeySet) ActiveVersion() int {
	if keys == nil {
		return 0
	}
	return keys.activeVersion
}

func newKeySet(activeVersion int, versions map[int]derivedKeys) *KeySet {
	active := versions[activeVersion]
	return &KeySet{activeVersion: activeVersion, versions: versions, aeadKey: active.aeadKey,
		fingerprintKey: active.fingerprintKey, accessKeyKey: active.accessKeyKey, sessionKey: active.sessionKey}
}

func deriveKeys(master [32]byte) (derivedKeys, error) {
	aeadKey, err := deriveKey(master, aeadKeyInfo)
	if err != nil {
		return derivedKeys{}, fmt.Errorf("derive AEAD key: %w", err)
	}
	fingerprintKey, err := deriveKey(master, fingerprintKeyInfo)
	if err != nil {
		return derivedKeys{}, fmt.Errorf("derive fingerprint key: %w", err)
	}
	accessKeyKey, err := deriveKey(master, accessKeyInfo)
	if err != nil {
		return derivedKeys{}, fmt.Errorf("derive access key digest key: %w", err)
	}
	sessionKey, err := deriveKey(master, sessionKeyInfo)
	if err != nil {
		return derivedKeys{}, fmt.Errorf("derive session digest key: %w", err)
	}

	return derivedKeys{
		aeadKey:        aeadKey,
		fingerprintKey: fingerprintKey,
		accessKeyKey:   accessKeyKey,
		sessionKey:     sessionKey,
	}, nil
}

func deriveKey(master [32]byte, info string) ([derivedKeyLength]byte, error) {
	derived, err := hkdf.Key(sha256.New, master[:], nil, info, derivedKeyLength)
	if err != nil {
		return [derivedKeyLength]byte{}, fmt.Errorf("HKDF with info %q: %w", info, err)
	}
	var key [derivedKeyLength]byte
	copy(key[:], derived)
	Zero(derived)
	return key, nil
}
