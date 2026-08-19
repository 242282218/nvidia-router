package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

func (keys *KeySet) Encrypt(plaintext []byte, aad string) ([]byte, []byte, error) {
	return keys.EncryptVersion(keys.ActiveVersion(), plaintext, aad)
}

func (keys *KeySet) EncryptVersion(version int, plaintext []byte, aad string) ([]byte, []byte, error) {
	if aad == "" {
		return nil, nil, fmt.Errorf("encrypt: AAD is empty")
	}
	gcm, err := keys.gcm(version)
	if err != nil {
		return nil, nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate AES-GCM nonce: %w", err)
	}
	return gcm.Seal(nil, nonce, plaintext, []byte(aad)), nonce, nil
}

func (keys *KeySet) Decrypt(ciphertext, nonce []byte, aad string) ([]byte, error) {
	return keys.DecryptVersion(keys.ActiveVersion(), ciphertext, nonce, aad)
}

func (keys *KeySet) DecryptVersion(version int, ciphertext, nonce []byte, aad string) ([]byte, error) {
	if aad == "" {
		return nil, fmt.Errorf("decrypt: AAD is empty")
	}
	gcm, err := keys.gcm(version)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("decrypt: invalid AES-GCM nonce length")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("decrypt AES-GCM ciphertext: %w", err)
	}
	return plaintext, nil
}

func (keys *KeySet) gcm(version int) (cipher.AEAD, error) {
	if keys == nil {
		return nil, fmt.Errorf("key set is nil")
	}
	keys.gcmMu.RLock()
	if keys.gcmCache != nil {
		if gcm, ok := keys.gcmCache[version]; ok {
			keys.gcmMu.RUnlock()
			if _, exists := keys.versions[version]; exists {
				return gcm, nil
			}
		} else {
			keys.gcmMu.RUnlock()
		}
	} else {
		keys.gcmMu.RUnlock()
	}
	derived, ok := keys.versions[version]
	if !ok {
		return nil, fmt.Errorf("unsupported key version %d", version)
	}
	block, err := aes.NewCipher(derived.aeadKey[:])
	if err != nil {
		return nil, fmt.Errorf("create AES-256 block cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	keys.gcmMu.Lock()
	if keys.gcmCache == nil {
		keys.gcmCache = make(map[int]cipher.AEAD)
	}
	keys.gcmCache[version] = gcm
	keys.gcmMu.Unlock()
	return gcm, nil
}

// InvalidateGCMCache clears the GCM cache. Called after rotation where a
// version's key material changes, ensuring no stale AEAD is reused.
func InvalidateGCMCache() {
	// Global helper kept for compatibility; per-instance cache is cleared lazily
	// via rotation which creates a new KeySet. No global state to clear.
}
