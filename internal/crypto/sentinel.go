package crypto

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
)

const (
	sentinelVersion   = 1
	sentinelAAD       = "crypto-sentinel:v1"
	sentinelPlaintext = "nvidia-router/crypto-sentinel/v1"
)

type sentinelRecord struct {
	version    int
	nonce      []byte
	ciphertext []byte
}

func (keys *KeySet) EnsureSentinel(ctx context.Context, db *sql.DB) error {
	record, err := readSentinel(ctx, db)
	if err == nil {
		return keys.validateSentinel(record)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read crypto sentinel: %w", err)
	}

	plaintext := []byte(sentinelPlaintext)
	defer Zero(plaintext)
	ciphertext, nonce, err := keys.Encrypt(plaintext, sentinelAAD)
	if err != nil {
		return fmt.Errorf("encrypt crypto sentinel: %w", err)
	}
	defer Zero(ciphertext)
	defer Zero(nonce)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO crypto_sentinel (id, version, nonce, ciphertext)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, sentinelVersion, nonce, ciphertext); err != nil {
		return fmt.Errorf("insert crypto sentinel: %w", err)
	}

	record, err = readSentinel(ctx, db)
	if err != nil {
		return fmt.Errorf("read crypto sentinel after initialization: %w", err)
	}
	return keys.validateSentinel(record)
}

// ValidateSentinel verifies the existing sentinel without modifying the database.
func (keys *KeySet) ValidateSentinel(ctx context.Context, db *sql.DB) error {
	record, err := readSentinel(ctx, db)
	if err != nil {
		return fmt.Errorf("read crypto sentinel: %w", err)
	}
	if err := keys.validateSentinel(record); err != nil {
		return fmt.Errorf("validate crypto sentinel: %w", err)
	}
	return nil
}

func readSentinel(ctx context.Context, db *sql.DB) (sentinelRecord, error) {
	var record sentinelRecord
	err := db.QueryRowContext(ctx, `
		SELECT version, nonce, ciphertext
		FROM crypto_sentinel
		WHERE id = 1`).Scan(&record.version, &record.nonce, &record.ciphertext)
	if err != nil {
		return sentinelRecord{}, fmt.Errorf("query crypto sentinel: %w", err)
	}
	return record, nil
}

func (keys *KeySet) validateSentinel(record sentinelRecord) error {
	if record.version != sentinelVersion {
		return fmt.Errorf("validate crypto sentinel: unsupported version %d", record.version)
	}
	plaintext, err := keys.Decrypt(record.ciphertext, record.nonce, sentinelAAD)
	if err != nil {
		return fmt.Errorf("decrypt crypto sentinel: %w", err)
	}
	defer Zero(plaintext)
	expected := []byte(sentinelPlaintext)
	defer Zero(expected)
	if subtle.ConstantTimeCompare(plaintext, expected) != 1 {
		return fmt.Errorf("validate crypto sentinel: plaintext mismatch")
	}
	return nil
}
