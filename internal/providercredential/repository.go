// Package providercredential stores OpenAI-compatible upstream credentials
// (base URL + API token) encrypted at rest under the router master key. It is
// the storage half of multi-provider support: NVIDIA keys keep their own store,
// while additional OpenAI-compatible providers (e.g. SiliconFlow) live here.
package providercredential

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/crypto"
)

const credentialAAD = "provider-credential:v1"

var ErrNotFound = errors.New("provider credential not found")

// Provider is the OpenAI-compatible provider family this store serves.
type Provider struct {
	ID            int64
	Name          string // stable route name, e.g. "siliconflow"
	BaseURL       string
	DisplayPrefix string
	DisplaySuffix string
	Enabled       bool
	CooldownUntil *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Repository persists provider credentials. For the first layer the runtime
// reads credentials into an in-memory registry; failure/cooldown bookkeeping is
// intentionally minimal here and will grow with the pool-routing layer.
type Repository struct {
	db    *sql.DB
	clock clock.Clock
	keys  *crypto.KeySet
}

func NewRepository(db *sql.DB, source clock.Clock, keys *crypto.KeySet) *Repository {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Repository{db: db, clock: source, keys: keys}
}

// Create encrypts and stores a provider credential, returning the stored row.
func (r *Repository) Create(ctx context.Context, name, baseURL, token string) (Provider, error) {
	now := r.clock.Now().UTC().Truncate(time.Second)
	tokenBytes := []byte(token)
	ciphertext, nonce, err := r.keys.Encrypt(tokenBytes, credentialAAD)
	fingerprint := r.keys.Fingerprint(tokenBytes)
	crypto.Zero(tokenBytes)
	if err != nil {
		crypto.Zero(fingerprint)
		return Provider{}, fmt.Errorf("encrypt provider credential: %w", err)
	}
	prefix, suffix := mask(token)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO provider_credentials (
			name, base_url, ciphertext, nonce, fingerprint,
			display_prefix, display_suffix, key_version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			base_url = excluded.base_url,
			ciphertext = excluded.ciphertext,
			nonce = excluded.nonce,
			fingerprint = excluded.fingerprint,
			display_prefix = excluded.display_prefix,
			display_suffix = excluded.display_suffix,
			key_version = excluded.key_version,
			enabled = 1,
			cooldown_until = NULL,
			cooldown_level = 0,
			consecutive_failures = 0,
			updated_at = excluded.updated_at
	`, name, baseURL, ciphertext, nonce, fingerprint, prefix, suffix, r.keys.ActiveVersion(), timestamp(now), timestamp(now))
	crypto.Zero(fingerprint)
	if err != nil {
		return Provider{}, fmt.Errorf("insert provider credential: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Provider{}, fmt.Errorf("read provider credential id: %w", err)
	}
	row, err := r.Get(ctx, id)
	if err != nil {
		return Provider{}, fmt.Errorf("load stored provider credential: %w", err)
	}
	return row, nil
}

// Get reads a provider credential by id (safe metadata only).
func (r *Repository) Get(ctx context.Context, id int64) (Provider, error) {
	var row Provider
	var enabled int
	var cooldown, created, updated sql.NullString
	if err := r.db.QueryRowContext(ctx, `
		SELECT id, name, base_url, display_prefix, display_suffix, enabled, cooldown_until, created_at, updated_at
		FROM provider_credentials WHERE id = ?
	`, id).Scan(&row.ID, &row.Name, &row.BaseURL, &row.DisplayPrefix, &row.DisplaySuffix, &enabled, &cooldown, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Provider{}, ErrNotFound
		}
		return Provider{}, fmt.Errorf("get provider credential: %w", err)
	}
	row.Enabled = enabled != 0
	row.CreatedAt = parseTime(created)
	row.UpdatedAt = parseTime(updated)
	return row, nil
}

// List returns all provider credentials, newest first.
func (r *Repository) List(ctx context.Context) ([]Provider, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, base_url, display_prefix, display_suffix, enabled, cooldown_until, created_at, updated_at
		FROM provider_credentials ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list provider credentials: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]Provider, 0)
	for rows.Next() {
		var row Provider
		var enabled int
		var cooldown, created, updated sql.NullString
		if err := rows.Scan(&row.ID, &row.Name, &row.BaseURL, &row.DisplayPrefix, &row.DisplaySuffix, &enabled, &cooldown, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan provider credential: %w", err)
		}
		row.Enabled = enabled != 0
		row.CreatedAt = parseTime(created)
		row.UpdatedAt = parseTime(updated)
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider credentials: %w", err)
	}
	return items, nil
}

// Resolve returns the encrypted material for a credential name so the request
// path can construct an OpenAI-compatible client with the real token.
func (r *Repository) Resolve(ctx context.Context, name string) (Provider, string, error) {
	var row Provider
	var enabled int
	var cooldown sql.NullString
	var created, updated string
	var ciphertext, nonce []byte
	var keyVersion int
	if err := r.db.QueryRowContext(ctx, `
		SELECT id, name, base_url, ciphertext, nonce, enabled, cooldown_until, key_version, created_at, updated_at
		FROM provider_credentials WHERE name = ? AND enabled = 1
	`, name).Scan(&row.ID, &row.Name, &row.BaseURL, &ciphertext, &nonce, &enabled, &cooldown, &keyVersion, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Provider{}, "", ErrNotFound
		}
		return Provider{}, "", fmt.Errorf("resolve provider credential: %w", err)
	}
	row.CreatedAt = mustParseTime(created)
	row.UpdatedAt = mustParseTime(updated)
	if keyVersion <= 0 {
		keyVersion = 1
	}
	tokenBytes, err := r.keys.DecryptVersion(keyVersion, ciphertext, nonce, credentialAAD)
	if err != nil {
		return Provider{}, "", fmt.Errorf("decrypt provider credential: %w", err)
	}
	defer crypto.Zero(tokenBytes)
	return row, string(tokenBytes), nil
}

// SetEnabled toggles a credential without touching encrypted material.
func (r *Repository) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := r.db.ExecContext(ctx, `UPDATE provider_credentials SET enabled = ?, updated_at = ? WHERE id = ?`, value, timestamp(r.clock.Now()), id)
	if err != nil {
		return fmt.Errorf("set provider credential enabled: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count provider credential update: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func mask(token string) (prefix, suffix string) {
	const visible = 4
	if len(token) <= visible*2 {
		prefix = token
		return prefix, ""
	}
	return token[:visible], token[len(token)-visible:]
}

func timestamp(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func parseTime(value sql.NullString) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value.String)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func mustParseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
