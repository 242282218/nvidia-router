package nvidiakey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nvidia-router/internal/fault"
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

func (r *Repository) List(ctx context.Context) ([]Key, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, display_prefix, display_suffix, enabled, auth_invalid,
		       cooldown_until, cooldown_reason, cooldown_level, consecutive_failures,
		       last_success_at, last_error_at, last_error_code, created_at, updated_at
		FROM nvidia_keys ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list NVIDIA keys: %w", err)
	}
	defer rows.Close()
	keys := make([]Key, 0)
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate NVIDIA keys: %w", err)
	}
	return keys, nil
}

func (r *Repository) FirstEnabledID(ctx context.Context) (int64, error) {
	var id int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT id FROM nvidia_keys
		WHERE enabled = 1 AND auth_invalid = 0
		ORDER BY id LIMIT 1
	`).Scan(&id); err != nil {
		return 0, fmt.Errorf("find first enabled NVIDIA key: %w", err)
	}
	return id, nil
}

func (r *Repository) SetEnabled(ctx context.Context, id int64, enabled bool, now time.Time) (keystate.KeySnapshot, error) {
	return r.stateTransaction(ctx, func(tx *sql.Tx) (keystate.KeySnapshot, error) {
		result, err := tx.ExecContext(ctx, `UPDATE nvidia_keys SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), formatTimestamp(now), id)
		if err != nil {
			return keystate.KeySnapshot{}, fmt.Errorf("set NVIDIA key enabled state: %w", err)
		}
		if err := requireOneRow(result, "set NVIDIA key enabled state"); err != nil {
			return keystate.KeySnapshot{}, err
		}
		return loadSchedulingSnapshot(ctx, tx, id)
	})
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM nvidia_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete NVIDIA key: %w", err)
	}
	return requireOneRow(result, "delete NVIDIA key")
}

type keyRowScanner interface{ Scan(...any) error }

func scanKey(row keyRowScanner) (Key, error) {
	var key Key
	var enabled, authInvalid int
	var cooldownUntil, lastSuccessAt, lastErrorAt sql.NullString
	var cooldownReason, lastErrorCode sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(
		&key.ID, &key.DisplayPrefix, &key.DisplaySuffix, &enabled, &authInvalid,
		&cooldownUntil, &cooldownReason, &key.CooldownLevel, &key.ConsecutiveFailures,
		&lastSuccessAt, &lastErrorAt, &lastErrorCode, &createdAt, &updatedAt,
	); err != nil {
		return Key{}, fmt.Errorf("scan NVIDIA key: %w", err)
	}
	key.Enabled = enabled == 1
	key.AuthInvalid = authInvalid == 1
	key.CooldownReason = cooldownReason.String
	key.LastErrorCode = lastErrorCode.String
	var err error
	if key.CooldownUntil, err = parseOptionalTimestamp(cooldownUntil); err != nil {
		return Key{}, fmt.Errorf("parse NVIDIA key cooldown: %w", err)
	}
	if key.LastSuccessAt, err = parseOptionalTimestamp(lastSuccessAt); err != nil {
		return Key{}, fmt.Errorf("parse NVIDIA key last success: %w", err)
	}
	if key.LastErrorAt, err = parseOptionalTimestamp(lastErrorAt); err != nil {
		return Key{}, fmt.Errorf("parse NVIDIA key last error: %w", err)
	}
	if key.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return Key{}, fmt.Errorf("parse NVIDIA key created time: %w", err)
	}
	if key.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return Key{}, fmt.Errorf("parse NVIDIA key updated time: %w", err)
	}
	return key, nil
}

func parseOptionalTimestamp(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
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
		snapshot, err := scanSchedulingSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("scan NVIDIA key scheduling snapshot: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate NVIDIA key scheduling snapshots: %w", err)
	}
	return snapshots, nil
}

func (r *Repository) markSuccess(ctx context.Context, keyID int64, now time.Time) (keystate.KeySnapshot, error) {
	return r.stateTransaction(ctx, func(tx *sql.Tx) (keystate.KeySnapshot, error) {
		result, err := tx.ExecContext(ctx, `
			UPDATE nvidia_keys SET
				cooldown_until = NULL,
				cooldown_reason = NULL,
				cooldown_level = 0,
				consecutive_failures = 0,
				last_success_at = ?,
				last_error_code = NULL,
				updated_at = ?
			WHERE id = ?
		`, formatTimestamp(now), formatTimestamp(now), keyID)
		if err != nil {
			return keystate.KeySnapshot{}, fmt.Errorf("update successful NVIDIA key state: %w", err)
		}
		if err := requireOneRow(result, "update successful NVIDIA key state"); err != nil {
			return keystate.KeySnapshot{}, err
		}
		return loadSchedulingSnapshot(ctx, tx, keyID)
	})
}

func (r *Repository) markFailure(
	ctx context.Context,
	keyID, modelID int64,
	f fault.Fault,
	now time.Time,
	random fault.RandomSource,
) (keystate.KeySnapshot, error) {
	return r.stateTransaction(ctx, func(tx *sql.Tx) (keystate.KeySnapshot, error) {
		current, err := loadSchedulingSnapshot(ctx, tx, keyID)
		if err != nil {
			return keystate.KeySnapshot{}, err
		}
		code := safePersistedCode(f.PublicCode)
		duration, nextLevel := fault.CalculateCooldown(f, current.CooldownLevel, random)
		authInvalid := current.AuthInvalid || f.DisableKey || f.HTTPStatus == 401
		recordCooldown := f.HTTPStatus == 429 || duration > 0
		if err := updateFailedKey(ctx, tx, keyID, authInvalid, code, duration, nextLevel, recordCooldown, now); err != nil {
			return keystate.KeySnapshot{}, err
		}
		if f.BlockModel {
			if err := upsertModelBlock(ctx, tx, keyID, modelID, code, f.HTTPStatus, now); err != nil {
				return keystate.KeySnapshot{}, err
			}
		}
		return loadSchedulingSnapshot(ctx, tx, keyID)
	})
}

func updateFailedKey(
	ctx context.Context,
	tx *sql.Tx,
	keyID int64,
	authInvalid bool,
	code string,
	duration time.Duration,
	nextLevel int,
	recordCooldown bool,
	now time.Time,
) error {
	timestamp := formatTimestamp(now)
	if recordCooldown {
		result, err := tx.ExecContext(ctx, `
			UPDATE nvidia_keys SET
				auth_invalid = ?, cooldown_until = ?, cooldown_reason = ?, cooldown_level = ?,
				consecutive_failures = consecutive_failures + 1,
				last_error_at = ?, last_error_code = ?, updated_at = ?
			WHERE id = ?
		`, boolInt(authInvalid), formatTimestamp(now.Add(duration)), code, nextLevel, timestamp, code, timestamp, keyID)
		if err != nil {
			return fmt.Errorf("update failed NVIDIA key cooldown: %w", err)
		}
		return requireOneRow(result, "update failed NVIDIA key cooldown")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE nvidia_keys SET
			auth_invalid = ?, last_error_at = ?, last_error_code = ?, updated_at = ?
		WHERE id = ?
	`, boolInt(authInvalid), timestamp, code, timestamp, keyID)
	if err != nil {
		return fmt.Errorf("update failed NVIDIA key state: %w", err)
	}
	return requireOneRow(result, "update failed NVIDIA key state")
}

func upsertModelBlock(
	ctx context.Context,
	tx *sql.Tx,
	keyID, modelID int64,
	reason string,
	status int,
	now time.Time,
) error {
	var upstreamStatus any
	if status > 0 {
		upstreamStatus = status
	}
	timestamp := formatTimestamp(now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO nvidia_key_model_blocks (
			nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(nvidia_key_id, model_id) DO UPDATE SET
			reason_code = excluded.reason_code,
			upstream_status = excluded.upstream_status,
			last_seen_at = excluded.last_seen_at
	`, keyID, modelID, reason, upstreamStatus, timestamp, timestamp); err != nil {
		return fmt.Errorf("upsert NVIDIA key model block: %w", err)
	}
	return nil
}

func (r *Repository) stateTransaction(
	ctx context.Context,
	operation func(*sql.Tx) (keystate.KeySnapshot, error),
) (snapshot keystate.KeySnapshot, returnErr error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return keystate.KeySnapshot{}, fmt.Errorf("begin NVIDIA key state transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			returnErr = fmt.Errorf("rollback NVIDIA key state transaction: %w", errors.Join(returnErr, rollbackErr))
		}
	}()

	snapshot, err = operation(tx)
	if err != nil {
		return keystate.KeySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return keystate.KeySnapshot{}, fmt.Errorf("commit NVIDIA key state transaction: %w", err)
	}
	committed = true
	return snapshot, nil
}

type schedulingSnapshotScanner interface {
	Scan(...any) error
}

func loadSchedulingSnapshot(ctx context.Context, tx *sql.Tx, keyID int64) (keystate.KeySnapshot, error) {
	snapshot, err := scanSchedulingSnapshot(tx.QueryRowContext(ctx, `
		SELECT id, enabled, auth_invalid, cooldown_until, cooldown_level, consecutive_failures
		FROM nvidia_keys WHERE id = ?
	`, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return keystate.KeySnapshot{}, fmt.Errorf("load NVIDIA key scheduling snapshot: %w", sql.ErrNoRows)
	}
	if err != nil {
		return keystate.KeySnapshot{}, fmt.Errorf("load NVIDIA key scheduling snapshot: %w", err)
	}
	return snapshot, nil
}

func scanSchedulingSnapshot(scanner schedulingSnapshotScanner) (keystate.KeySnapshot, error) {
	var snapshot keystate.KeySnapshot
	var enabled, authInvalid int
	var cooldownUntil sql.NullString
	if err := scanner.Scan(
		&snapshot.ID, &enabled, &authInvalid, &cooldownUntil,
		&snapshot.CooldownLevel, &snapshot.ConsecutiveFailures,
	); err != nil {
		return keystate.KeySnapshot{}, err
	}
	snapshot.Enabled = enabled == 1
	snapshot.AuthInvalid = authInvalid == 1
	if cooldownUntil.Valid {
		parsed, err := time.Parse(time.RFC3339, cooldownUntil.String)
		if err != nil {
			return keystate.KeySnapshot{}, fmt.Errorf("parse NVIDIA key cooldown time: %w", err)
		}
		snapshot.CooldownUntil = &parsed
	}
	return snapshot, nil
}

func requireOneRow(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: count affected rows: %w", operation, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s: expected one row, updated %d", operation, rows)
	}
	return nil
}

func safePersistedCode(code string) string {
	if code == "" || len(code) > 128 {
		return "upstream_error"
	}
	for _, character := range code {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return "upstream_error"
		}
	}
	return code
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}
