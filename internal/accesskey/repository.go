package accesskey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Repository struct {
	db     *sql.DB
	reader *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// WithReader routes read-only queries to a separate connection pool. The writer
// pool is capped at one connection, so without this every authentication queued
// behind in-flight writes.
func (r *Repository) WithReader(reader *sql.DB) *Repository {
	clone := *r
	clone.reader = reader
	return &clone
}

func (r *Repository) read() *sql.DB {
	if r.reader != nil {
		return r.reader
	}
	return r.db
}

func (r *Repository) Create(ctx context.Context, name string, digest []byte, prefix string, now time.Time, digestVersions ...int) (Key, error) {
	digestVersion := 1
	if len(digestVersions) > 0 && digestVersions[0] > 0 {
		digestVersion = digestVersions[0]
	}
	timestamp := formatTime(now)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO access_keys (name, key_digest, key_prefix, digest_key_version, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, name, digest, prefix, digestVersion, timestamp)
	if err != nil {
		return Key{}, fmt.Errorf("insert access key: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Key{}, fmt.Errorf("read inserted access key ID: %w", err)
	}
	return Key{ID: id, Name: name, Prefix: prefix, CreatedAt: now.UTC().Truncate(time.Second)}, nil
}

func (r *Repository) List(ctx context.Context) ([]Key, error) {
	rows, err := r.read().QueryContext(ctx, `
		SELECT id, name, key_prefix, created_at, last_used_at, revoked_at, expires_at, rpm_limit, tpm_limit, max_concurrent, token_budget, consumed_tokens
		FROM access_keys
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list access keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([]Key, 0)
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access keys: %w", err)
	}
	return keys, nil
}

func (r *Repository) Authenticate(ctx context.Context, digests map[int][]byte) (AccessKeyIdentity, int, error) {
	if len(digests) == 0 {
		return AccessKeyIdentity{}, 0, ErrInvalidAccessKey
	}
	var identity AccessKeyIdentity
	var digestKeyVersion int
	var expiresAt sql.NullString
	for version, digest := range digests {
		err := r.read().QueryRowContext(ctx, `
			SELECT id, key_prefix, digest_key_version, expires_at, rpm_limit, tpm_limit, max_concurrent, token_budget, consumed_tokens
			FROM access_keys
			WHERE key_digest = ? AND revoked_at IS NULL
		`, digest).Scan(&identity.ID, &identity.Prefix, &digestKeyVersion, &expiresAt, &identity.RPMLimit, &identity.TPMLimit, &identity.MaxConcurrent, &identity.TokenBudget, &identity.ConsumedTokens)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return AccessKeyIdentity{}, 0, fmt.Errorf("authenticate access key: %w", err)
		}
		var parseErr error
		identity.ExpiresAt, parseErr = parseOptionalTime(expiresAt)
		if parseErr != nil {
			return AccessKeyIdentity{}, 0, fmt.Errorf("parse access key expiration: %w", parseErr)
		}
		return identity, version, nil
	}
	return AccessKeyIdentity{}, 0, ErrInvalidAccessKey
}

func (r *Repository) UpdateDigest(ctx context.Context, id int64, digest []byte, version int) error {
	result, err := r.db.ExecContext(ctx, `UPDATE access_keys SET key_digest = ?, digest_key_version = ? WHERE id = ? AND revoked_at IS NULL`, digest, version, id)
	if err != nil {
		return fmt.Errorf("update access key digest: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated access key digest count: %w", err)
	}
	if changed == 0 {
		return ErrAccessKeyNotFound
	}
	return nil
}

func (r *Repository) Revoke(ctx context.Context, id int64, now time.Time) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE access_keys
		SET revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL
	`, formatTime(now), id); err != nil {
		return fmt.Errorf("revoke access key: %w", err)
	}
	return nil
}

func (r *Repository) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time, minimumInterval time.Duration) error {
	threshold := usedAt.Add(-minimumInterval)
	if _, err := r.db.ExecContext(ctx, `
		UPDATE access_keys
		SET last_used_at = ?
		WHERE id = ? AND revoked_at IS NULL
		  AND (last_used_at IS NULL OR last_used_at <= ?)
	`, formatTime(usedAt), id, formatTime(threshold)); err != nil {
		return fmt.Errorf("update access key last used time: %w", err)
	}
	return nil
}

func (r *Repository) UpdatePolicy(ctx context.Context, id int64, expiresAt *time.Time, rpm, tpm, maxConcurrent int, tokenBudget *int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE access_keys SET expires_at = ?, rpm_limit = ?, tpm_limit = ?, max_concurrent = ?, token_budget = COALESCE(?, token_budget) WHERE id = ? AND revoked_at IS NULL`, optionalTime(expiresAt), rpm, tpm, maxConcurrent, optionalInt64(tokenBudget), id)
	if err != nil {
		return fmt.Errorf("update access key policy: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated access key count: %w", err)
	}
	if changed == 0 {
		return ErrAccessKeyNotFound
	}
	return nil
}

func optionalInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// UpdateConsumedTokens persists the in-memory budget counter back to the row.
// The write is best-effort: budget enforcement reads the in-memory limiter, so
// a stale value here only affects restart recovery and the admin display.
func (r *Repository) UpdateConsumedTokens(ctx context.Context, id int64, consumed int64) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE access_keys SET consumed_tokens = ? WHERE id = ? AND revoked_at IS NULL`, consumed, id); err != nil {
		return fmt.Errorf("update access key consumed tokens: %w", err)
	}
	return nil
}

func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanKey(row rowScanner) (Key, error) {
	var key Key
	var createdAt string
	var lastUsedAt sql.NullString
	var revokedAt sql.NullString
	var expiresAt sql.NullString
	if err := row.Scan(&key.ID, &key.Name, &key.Prefix, &createdAt, &lastUsedAt, &revokedAt, &expiresAt, &key.RPMLimit, &key.TPMLimit, &key.MaxConcurrent, &key.TokenBudget, &key.ConsumedTokens); err != nil {
		return Key{}, fmt.Errorf("scan access key: %w", err)
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return Key{}, fmt.Errorf("parse access key created time: %w", err)
	}
	key.CreatedAt = parsedCreatedAt
	if key.LastUsedAt, err = parseOptionalTime(lastUsedAt); err != nil {
		return Key{}, fmt.Errorf("parse access key last used time: %w", err)
	}
	if key.RevokedAt, err = parseOptionalTime(revokedAt); err != nil {
		return Key{}, fmt.Errorf("parse access key revoked time: %w", err)
	}
	if key.ExpiresAt, err = parseOptionalTime(expiresAt); err != nil {
		return Key{}, fmt.Errorf("parse access key expiration: %w", err)
	}
	return key, nil
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}
