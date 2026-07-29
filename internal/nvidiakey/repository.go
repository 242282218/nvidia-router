package nvidiakey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nvidia-router/internal/keystate"
)

type Repository struct {
	db *sql.DB
}

type encryptedKey struct {
	ciphertext []byte
	nonce      []byte
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) FingerprintExists(ctx context.Context, fingerprint []byte) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM nvidia_keys WHERE fingerprint = ?", fingerprint).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query NVIDIA key fingerprint: %w", err)
	}
	return true, nil
}

func (r *Repository) Create(
	ctx context.Context,
	ciphertext, nonce, fingerprint []byte,
	prefix, suffix string,
	now time.Time,
) (Key, bool, error) {
	timestamp := formatTimestamp(now)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix,
			enabled, auth_invalid, cooldown_level, consecutive_failures,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 1, 0, 0, 0, ?, ?)
		ON CONFLICT(fingerprint) DO NOTHING
	`, ciphertext, nonce, fingerprint, prefix, suffix, timestamp, timestamp)
	if err != nil {
		return Key{}, false, fmt.Errorf("insert NVIDIA key: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Key{}, false, fmt.Errorf("count inserted NVIDIA key rows: %w", err)
	}
	if rows == 0 {
		return Key{}, true, nil
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Key{}, false, fmt.Errorf("read inserted NVIDIA key ID: %w", err)
	}
	timeValue := now.UTC().Truncate(time.Second)
	return Key{
		ID:            id,
		DisplayPrefix: prefix,
		DisplaySuffix: suffix,
		Enabled:       true,
		CreatedAt:     timeValue,
		UpdatedAt:     timeValue,
	}, false, nil
}

func (r *Repository) LoadEncrypted(ctx context.Context, id int64) (encryptedKey, error) {
	var value encryptedKey
	err := r.db.QueryRowContext(ctx, `
		SELECT ciphertext, nonce
		FROM nvidia_keys
		WHERE id = ?
	`, id).Scan(&value.ciphertext, &value.nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return encryptedKey{}, fmt.Errorf("load NVIDIA key ciphertext: %w", sql.ErrNoRows)
	}
	if err != nil {
		return encryptedKey{}, fmt.Errorf("load NVIDIA key ciphertext: %w", err)
	}
	return value, nil
}

func (r *Repository) ListSnapshots(ctx context.Context) ([]keystate.KeySnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, enabled, auth_invalid, cooldown_until, cooldown_level, consecutive_failures
		FROM nvidia_keys
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list NVIDIA key scheduling snapshots: %w", err)
	}
	defer rows.Close()
	snapshots := make([]keystate.KeySnapshot, 0)
	for rows.Next() {
		var snapshot keystate.KeySnapshot
		var enabled, authInvalid int
		var cooldownUntil sql.NullString
		if err := rows.Scan(&snapshot.ID, &enabled, &authInvalid, &cooldownUntil, &snapshot.CooldownLevel, &snapshot.ConsecutiveFailures); err != nil {
			return nil, fmt.Errorf("scan NVIDIA key scheduling snapshot: %w", err)
		}
		snapshot.Enabled = enabled == 1
		snapshot.AuthInvalid = authInvalid == 1
		if cooldownUntil.Valid {
			parsed, err := time.Parse(time.RFC3339, cooldownUntil.String)
			if err != nil {
				return nil, fmt.Errorf("parse NVIDIA key cooldown time: %w", err)
			}
			snapshot.CooldownUntil = &parsed
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate NVIDIA key scheduling snapshots: %w", err)
	}
	return snapshots, nil
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}
