package crypto

import (
	"context"
	"testing"
)

func TestRotateDatabaseReencryptsSecretsAndIsIdempotent(t *testing.T) {
	db := newTestDatabase(t)
	oldKeys := newTestKeySet(t, 1)
	newKeys, err := NewVersioned(2, [32]byte{2})
	if err != nil {
		t.Fatalf("NewVersioned: %v", err)
	}
	if err := oldKeys.EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("EnsureSentinel: %v", err)
	}
	secret := []byte("nvapi-rotation-secret")
	ciphertext, nonce, err := oldKeys.Encrypt(secret, nvidiaKeyRotationAAD)
	if err != nil {
		t.Fatalf("encrypt NVIDIA secret: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO nvidia_keys (ciphertext, nonce, fingerprint, display_prefix, display_suffix, key_version, created_at, updated_at) VALUES (?, ?, X'01', 'nvapi-', 'secret', 1, '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z')`, ciphertext, nonce); err != nil {
		t.Fatalf("insert NVIDIA secret: %v", err)
	}
	result, err := RotateDatabase(context.Background(), db, oldKeys, newKeys)
	if err != nil {
		t.Fatalf("RotateDatabase: %v", err)
	}
	if result.NVIDIAKeys != 1 || !result.Sentinel {
		t.Fatalf("rotation result = %+v", result)
	}
	if err := newKeys.ValidateSentinel(context.Background(), db); err != nil {
		t.Fatalf("validate new sentinel: %v", err)
	}
	var rotatedCiphertext, rotatedNonce []byte
	var version int
	if err := db.QueryRow("SELECT ciphertext, nonce, key_version FROM nvidia_keys WHERE id = 1").Scan(&rotatedCiphertext, &rotatedNonce, &version); err != nil {
		t.Fatalf("read rotated secret: %v", err)
	}
	if version != 2 {
		t.Fatalf("rotated key version = %d", version)
	}
	plaintext, err := newKeys.Decrypt(rotatedCiphertext, rotatedNonce, nvidiaKeyRotationAAD)
	if err != nil || string(plaintext) != string(secret) {
		t.Fatalf("rotated plaintext = %q/%v", plaintext, err)
	}
	second, err := RotateDatabase(context.Background(), db, oldKeys, newKeys)
	if err != nil {
		t.Fatalf("second RotateDatabase: %v", err)
	}
	if second.NVIDIAKeys != 0 {
		t.Fatalf("second rotation changed %d NVIDIA keys", second.NVIDIAKeys)
	}
}

func TestRotateDatabaseRollsBackOnWrongOldKey(t *testing.T) {
	db := newTestDatabase(t)
	oldKeys := newTestKeySet(t, 1)
	wrongKeys := newTestKeySet(t, 3)
	newKeys, err := NewVersioned(2, [32]byte{2})
	if err != nil {
		t.Fatalf("NewVersioned: %v", err)
	}
	if err := oldKeys.EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("EnsureSentinel: %v", err)
	}
	before := readSentinelSnapshot(t, db)
	if _, err := RotateDatabase(context.Background(), db, wrongKeys, newKeys); err == nil {
		t.Fatal("RotateDatabase with wrong old key succeeded")
	}
	after := readSentinelSnapshot(t, db)
	if before.keyVersion != after.keyVersion || string(before.ciphertext) != string(after.ciphertext) {
		t.Fatal("failed rotation modified sentinel")
	}
}
