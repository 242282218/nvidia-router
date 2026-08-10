package crypto

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	nvidiaKeyRotationAAD = "nvidia-key:v1"
	proxyKeyRotationAAD  = "proxy-pool-auth-key:v1"
)

// RotateDatabase re-encrypts all reversible secrets in one transaction. It is
// deliberately offline: callers must provide both old and new key sets and
// ensure no serving process is writing the database concurrently.
func RotateDatabase(ctx context.Context, db *sql.DB, oldKeys, newKeys *KeySet) (RotationResult, error) {
	if db == nil || oldKeys == nil || newKeys == nil {
		return RotationResult{}, errors.New("rotate database: database and key sets are required")
	}
	if oldKeys.ActiveVersion() == newKeys.ActiveVersion() {
		return RotationResult{}, errors.New("rotate database: key versions must differ")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RotationResult{}, fmt.Errorf("rotate database: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result := RotationResult{}
	if err := rotateNVIDIAKeys(ctx, tx, oldKeys, newKeys, &result); err != nil {
		return RotationResult{}, err
	}
	if err := rotateProxyKey(ctx, tx, oldKeys, newKeys, &result); err != nil {
		return RotationResult{}, err
	}
	if err := rotateSentinel(ctx, tx, oldKeys, newKeys); err != nil {
		return RotationResult{}, err
	}
	if err := countLegacyDigests(ctx, tx, newKeys.ActiveVersion(), &result); err != nil {
		return RotationResult{}, err
	}
	result.Sentinel = true
	if err := tx.Commit(); err != nil {
		return RotationResult{}, fmt.Errorf("rotate database: commit: %w", err)
	}
	committed = true
	return result, nil
}

type RotationResult struct {
	NVIDIAKeys    int
	ProxyKey      bool
	Sentinel      bool
	LegacyDigests int
}

func countLegacyDigests(ctx context.Context, tx *sql.Tx, activeVersion int, result *RotationResult) error {
	var accessCount, sessionCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM access_keys WHERE digest_key_version <> ?", activeVersion).Scan(&accessCount); err != nil {
		return fmt.Errorf("count legacy access key digests: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_sessions WHERE digest_key_version <> ? AND revoked_at IS NULL AND expires_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now')", activeVersion).Scan(&sessionCount); err != nil {
		return fmt.Errorf("count legacy admin session digests: %w", err)
	}
	result.LegacyDigests = accessCount + sessionCount
	return nil
}

func rotateNVIDIAKeys(ctx context.Context, tx *sql.Tx, oldKeys, newKeys *KeySet, result *RotationResult) error {
	rows, err := tx.QueryContext(ctx, "SELECT id, ciphertext, nonce, key_version FROM nvidia_keys WHERE key_version <> ?", newKeys.ActiveVersion())
	if err != nil {
		return fmt.Errorf("rotate NVIDIA keys: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, version int
		var ciphertext, nonce []byte
		if err := rows.Scan(&id, &ciphertext, &nonce, &version); err != nil {
			return fmt.Errorf("rotate NVIDIA keys: scan: %w", err)
		}
		plaintext, err := oldKeys.DecryptVersion(version, ciphertext, nonce, nvidiaKeyRotationAAD)
		if err != nil {
			return fmt.Errorf("rotate NVIDIA key %d: decrypt: %w", id, err)
		}
		newCiphertext, newNonce, err := newKeys.Encrypt(plaintext, nvidiaKeyRotationAAD)
		if err != nil {
			Zero(plaintext)
			return fmt.Errorf("rotate NVIDIA key %d: encrypt: %w", id, err)
		}
		fingerprint := newKeys.Fingerprint(plaintext)
		Zero(plaintext)
		if _, err := tx.ExecContext(ctx, "UPDATE nvidia_keys SET ciphertext = ?, nonce = ?, fingerprint = ?, key_version = ?, updated_at = ? WHERE id = ?", newCiphertext, newNonce, fingerprint, newKeys.ActiveVersion(), time.Now().UTC().Format(time.RFC3339), id); err != nil {
			Zero(fingerprint)
			Zero(newCiphertext)
			Zero(newNonce)
			return fmt.Errorf("rotate NVIDIA key %d: update: %w", id, err)
		}
		Zero(fingerprint)
		Zero(newCiphertext)
		Zero(newNonce)
		result.NVIDIAKeys++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rotate NVIDIA keys: iterate: %w", err)
	}
	return nil
}

func rotateProxyKey(ctx context.Context, tx *sql.Tx, oldKeys, newKeys *KeySet, result *RotationResult) error {
	var ciphertext, nonce []byte
	var version int
	err := tx.QueryRowContext(ctx, "SELECT auth_key_ciphertext, auth_key_nonce, key_version FROM proxy_pool_settings WHERE id = 1 AND auth_key_ciphertext IS NOT NULL").Scan(&ciphertext, &nonce, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if version == newKeys.ActiveVersion() {
		return nil
	}
	if err != nil {
		return fmt.Errorf("rotate proxy key: query: %w", err)
	}
	plaintext, err := oldKeys.DecryptVersion(version, ciphertext, nonce, proxyKeyRotationAAD)
	if err != nil {
		return fmt.Errorf("rotate proxy key: decrypt: %w", err)
	}
	newCiphertext, newNonce, err := newKeys.Encrypt(plaintext, proxyKeyRotationAAD)
	Zero(plaintext)
	if err != nil {
		return fmt.Errorf("rotate proxy key: encrypt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE proxy_pool_settings SET auth_key_ciphertext = ?, auth_key_nonce = ?, key_version = ?, updated_at = ? WHERE id = 1", newCiphertext, newNonce, newKeys.ActiveVersion(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		Zero(newCiphertext)
		Zero(newNonce)
		return fmt.Errorf("rotate proxy key: update: %w", err)
	}
	Zero(newCiphertext)
	Zero(newNonce)
	result.ProxyKey = true
	return nil
}

func rotateSentinel(ctx context.Context, tx *sql.Tx, oldKeys, newKeys *KeySet) error {
	var payloadVersion, version int
	var ciphertext, nonce []byte
	if err := tx.QueryRowContext(ctx, "SELECT version, key_version, nonce, ciphertext FROM crypto_sentinel WHERE id = 1").Scan(&payloadVersion, &version, &nonce, &ciphertext); err != nil {
		return fmt.Errorf("rotate crypto sentinel: query: %w", err)
	}
	if version == newKeys.ActiveVersion() {
		return nil
	}
	if payloadVersion != sentinelVersion {
		return fmt.Errorf("rotate crypto sentinel: unsupported payload version %d", payloadVersion)
	}
	plaintext, err := oldKeys.DecryptVersion(version, ciphertext, nonce, sentinelAAD)
	if err != nil {
		return fmt.Errorf("rotate crypto sentinel: decrypt: %w", err)
	}
	newCiphertext, newNonce, err := newKeys.Encrypt(plaintext, sentinelAAD)
	Zero(plaintext)
	if err != nil {
		return fmt.Errorf("rotate crypto sentinel: encrypt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE crypto_sentinel SET ciphertext = ?, nonce = ?, key_version = ? WHERE id = 1", newCiphertext, newNonce, newKeys.ActiveVersion()); err != nil {
		Zero(newCiphertext)
		Zero(newNonce)
		return fmt.Errorf("rotate crypto sentinel: update: %w", err)
	}
	Zero(newCiphertext)
	Zero(newNonce)
	return nil
}
