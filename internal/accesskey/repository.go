package accesskey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, name string, digest []byte, prefix string, now time.Time) (Key, error) {
	timestamp := formatTime(now)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO access_keys (name, key_digest, key_prefix, created_at)
		VALUES (?, ?, ?, ?)
	`, name, digest, prefix, timestamp)
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
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, key_prefix, created_at, last_used_at, revoked_at
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

func (r *Repository) Authenticate(ctx context.Context, digest []byte) (AccessKeyIdentity, error) {
	var identity AccessKeyIdentity
	err := r.db.QueryRowContext(ctx, `
		SELECT id, key_prefix
		FROM access_keys
		WHERE key_digest = ? AND revoked_at IS NULL
	`, digest).Scan(&identity.ID, &identity.Prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessKeyIdentity{}, ErrInvalidAccessKey
	}
	if err != nil {
		return AccessKeyIdentity{}, fmt.Errorf("authenticate access key: %w", err)
	}
	return identity, nil
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanKey(row rowScanner) (Key, error) {
	var key Key
	var createdAt string
	var lastUsedAt sql.NullString
	var revokedAt sql.NullString
	if err := row.Scan(&key.ID, &key.Name, &key.Prefix, &createdAt, &lastUsedAt, &revokedAt); err != nil {
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
