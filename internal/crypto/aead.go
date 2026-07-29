package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

func (keys *KeySet) Encrypt(plaintext []byte, aad string) ([]byte, []byte, error) {
	if aad == "" {
		return nil, nil, fmt.Errorf("encrypt: AAD is empty")
	}
	gcm, err := keys.gcm()
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
	if aad == "" {
		return nil, fmt.Errorf("decrypt: AAD is empty")
	}
	gcm, err := keys.gcm()
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

func (keys *KeySet) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(keys.aeadKey[:])
	if err != nil {
		return nil, fmt.Errorf("create AES-256 block cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return gcm, nil
}
