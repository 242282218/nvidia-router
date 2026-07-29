package crypto

import (
	"bytes"
	"context"
	"database/sql"
	"sync"
	"testing"

	"nvidia-router/internal/database"
)

const testAAD = "nvidia-key:v1"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	keys := newTestKeySet(t, 1)
	plaintext := []byte("nvapi-test-secret")

	ciphertext, nonce, err := keys.Encrypt(plaintext, testAAD)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := keys.Decrypt(ciphertext, nonce, testAAD)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesDifferentNonceForSamePlaintext(t *testing.T) {
	keys := newTestKeySet(t, 1)
	plaintext := []byte("nvapi-test-secret")

	firstCiphertext, firstNonce, err := keys.Encrypt(plaintext, testAAD)
	if err != nil {
		t.Fatalf("first Encrypt: %v", err)
	}
	secondCiphertext, secondNonce, err := keys.Encrypt(plaintext, testAAD)
	if err != nil {
		t.Fatalf("second Encrypt: %v", err)
	}
	if bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("Encrypt() reused a nonce")
	}
	if bytes.Equal(firstCiphertext, secondCiphertext) {
		t.Fatal("Encrypt() produced identical ciphertext")
	}
}

func TestDecryptRejectsWrongMasterKey(t *testing.T) {
	keys := newTestKeySet(t, 1)
	ciphertext, nonce, err := keys.Encrypt([]byte("nvapi-test-secret"), testAAD)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := newTestKeySet(t, 2).Decrypt(ciphertext, nonce, testAAD); err == nil {
		t.Fatal("Decrypt() with wrong master key succeeded")
	}
}

func TestDecryptRejectsDifferentAAD(t *testing.T) {
	keys := newTestKeySet(t, 1)
	ciphertext, nonce, err := keys.Encrypt([]byte("nvapi-test-secret"), testAAD)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := keys.Decrypt(ciphertext, nonce, "nvidia-key:v2"); err == nil {
		t.Fatal("Decrypt() with different AAD succeeded")
	}
}

func TestDigestUsesStableSeparatedKeys(t *testing.T) {
	keys := newTestKeySet(t, 1)
	value := []byte("nvapi-test-secret")

	fingerprint := keys.Fingerprint(value)
	if !bytes.Equal(fingerprint, keys.Fingerprint(value)) {
		t.Fatal("Fingerprint() is not stable")
	}
	if bytes.Equal(fingerprint, keys.AccessKeyDigest(value)) {
		t.Fatal("Fingerprint() and AccessKeyDigest() use the same derived key")
	}
	if bytes.Equal(fingerprint, keys.SessionDigest(value)) {
		t.Fatal("Fingerprint() and SessionDigest() use the same derived key")
	}
	if bytes.Equal(keys.AccessKeyDigest(value), keys.SessionDigest(value)) {
		t.Fatal("AccessKeyDigest() and SessionDigest() use the same derived key")
	}
}

func TestEnsureSentinelCreatesAndValidates(t *testing.T) {
	db := newTestDatabase(t)
	keys := newTestKeySet(t, 1)

	if err := keys.EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("first EnsureSentinel: %v", err)
	}
	if err := keys.EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("second EnsureSentinel: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM crypto_sentinel").Scan(&count); err != nil {
		t.Fatalf("count crypto_sentinel: %v", err)
	}
	if count != 1 {
		t.Fatalf("crypto_sentinel count = %d, want 1", count)
	}
}

func TestEnsureSentinelRejectsWrongMasterKey(t *testing.T) {
	db := newTestDatabase(t)
	if err := newTestKeySet(t, 1).EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("EnsureSentinel with correct key: %v", err)
	}

	if err := newTestKeySet(t, 2).EnsureSentinel(context.Background(), db); err == nil {
		t.Fatal("EnsureSentinel() with wrong master key succeeded")
	}
}

func TestEnsureSentinelConcurrentInitializationConverges(t *testing.T) {
	db := newTestDatabase(t)
	keys := newTestKeySet(t, 1)
	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- keys.EnsureSentinel(context.Background(), db)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent EnsureSentinel: %v", err)
		}
	}
}

func TestZeroClearsBytes(t *testing.T) {
	secret := []byte("nvapi-test-secret")
	Zero(secret)
	if !bytes.Equal(secret, make([]byte, len(secret))) {
		t.Fatalf("Zero() = %v, want all zero bytes", secret)
	}
}

func TestEncryptRejectsEmptyAAD(t *testing.T) {
	if _, _, err := newTestKeySet(t, 1).Encrypt([]byte("nvapi-test-secret"), ""); err == nil {
		t.Fatal("Encrypt() with empty AAD succeeded")
	}
}

func newTestKeySet(t *testing.T, firstByte byte) *KeySet {
	t.Helper()
	var master [32]byte
	master[0] = firstByte
	keys, err := New(master)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return keys
}

func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(t.TempDir() + "/router.db")
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}
