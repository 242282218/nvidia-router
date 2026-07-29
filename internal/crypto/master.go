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
	aeadKey        [derivedKeyLength]byte
	fingerprintKey [derivedKeyLength]byte
	accessKeyKey   [derivedKeyLength]byte
	sessionKey     [derivedKeyLength]byte
}

func New(master [32]byte) (*KeySet, error) {
	aeadKey, err := deriveKey(master, aeadKeyInfo)
	if err != nil {
		return nil, fmt.Errorf("derive AEAD key: %w", err)
	}
	fingerprintKey, err := deriveKey(master, fingerprintKeyInfo)
	if err != nil {
		return nil, fmt.Errorf("derive fingerprint key: %w", err)
	}
	accessKeyKey, err := deriveKey(master, accessKeyInfo)
	if err != nil {
		return nil, fmt.Errorf("derive access key digest key: %w", err)
	}
	sessionKey, err := deriveKey(master, sessionKeyInfo)
	if err != nil {
		return nil, fmt.Errorf("derive session digest key: %w", err)
	}

	return &KeySet{
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
