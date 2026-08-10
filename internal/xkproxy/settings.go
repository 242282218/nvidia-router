package xkproxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nvidia-router/internal/crypto"
)

const proxyPoolAuthKeyAAD = "proxy-pool-auth-key:v1"

type Source string

const (
	SourceDatabase    Source = "database"
	SourceEnvironment Source = "environment"
	SourceNone        Source = "none"
)

type Snapshot struct {
	Enabled        bool   `json:"enabled"`
	ProxyURL       string `json:"proxy_url"`
	AuthConfigured bool   `json:"auth_configured"`
	Source         Source `json:"source"`
}

type ValidationError struct {
	message string
}

func (e *ValidationError) Error() string { return e.message }

func invalidSettings(message string) error {
	return &ValidationError{message: message}
}

type EnvironmentConfig struct {
	URL     *url.URL
	AuthKey string
}

type Patch struct {
	Enabled      *bool  `json:"enabled"`
	ProxyURL     string `json:"proxy_url"`
	AuthKey      string `json:"auth_key"`
	ClearAuthKey bool   `json:"clear_auth_key"`
}

type SettingsService struct {
	db       *sql.DB
	keys     *crypto.KeySet
	env      EnvironmentConfig
	base     *http.Transport
	logger   *slog.Logger
	switcher *Switcher

	mu      sync.Mutex
	current proxyConfig
	closed  bool
}

type proxyConfig struct {
	snapshot Snapshot
	proxyURL *url.URL
	authKey  string
}

type storedProxyConfig struct {
	enabled    bool
	proxyURL   string
	nonce      []byte
	ciphertext []byte
	version    int
	keyVersion int
}

func NewSettingsService(ctx context.Context, db *sql.DB, keys *crypto.KeySet, env EnvironmentConfig, base *http.Transport, logger *slog.Logger) (*SettingsService, error) {
	if db == nil {
		return nil, errors.New("initialize proxy settings: database is required")
	}
	if keys == nil {
		return nil, errors.New("initialize proxy settings: crypto keys are required")
	}
	if err := validateEnvironmentConfig(env); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	service := &SettingsService{db: db, keys: keys, env: cloneEnvironmentConfig(env), base: base, logger: logger}
	config, err := service.loadCurrent(ctx)
	if err != nil {
		return nil, err
	}
	var manager *Manager
	if config.snapshot.Enabled {
		manager, err = service.newManager(config)
		if err != nil {
			return nil, err
		}
	}
	service.current = config
	service.switcher = NewSwitcher(manager, config.snapshot.Enabled)
	return service, nil
}

func (s *SettingsService) Switcher() *Switcher {
	if s == nil {
		return nil
	}
	return s.switcher
}

func (s *SettingsService) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if s == nil {
		return Snapshot{}, errors.New("proxy settings service is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Snapshot{}, errors.New("proxy settings service is closed")
	}
	return s.current.snapshot, nil
}

func (s *SettingsService) Update(ctx context.Context, patch Patch) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if s == nil {
		return Snapshot{}, errors.New("proxy settings service is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Snapshot{}, errors.New("proxy settings service is closed")
	}

	next, err := s.applyPatch(patch)
	if err != nil {
		return Snapshot{}, err
	}
	var manager *Manager
	if next.snapshot.Enabled {
		manager, err = s.newManager(next)
		if err != nil {
			return Snapshot{}, err
		}
	}
	if err := s.persist(ctx, next); err != nil {
		if manager != nil {
			manager.Close()
		}
		return Snapshot{}, err
	}
	if err := s.switcher.Apply(manager, next.snapshot.Enabled); err != nil {
		return Snapshot{}, fmt.Errorf("apply proxy settings: %w", err)
	}
	next.snapshot.Source = SourceDatabase
	s.current = next
	return next.snapshot, nil
}

func (s *SettingsService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	if s.switcher != nil {
		s.switcher.Close()
	}
}

func (s *SettingsService) loadCurrent(ctx context.Context) (proxyConfig, error) {
	stored, err := s.loadStored(ctx)
	if err != nil {
		return proxyConfig{}, err
	}
	if stored == nil {
		return environmentProxyConfig(s.env)
	}
	return s.databaseProxyConfig(*stored)
}

func (s *SettingsService) loadStored(ctx context.Context) (*storedProxyConfig, error) {
	var enabled int
	var stored storedProxyConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, key_version
		FROM proxy_pool_settings WHERE id = 1`).Scan(
		&enabled, &stored.proxyURL, &stored.nonce, &stored.ciphertext, &stored.version, &stored.keyVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load proxy settings: %w", err)
	}
	if enabled != 0 && enabled != 1 {
		return nil, errors.New("load proxy settings: enabled value is invalid")
	}
	stored.enabled = enabled == 1
	return &stored, nil
}

func (s *SettingsService) databaseProxyConfig(stored storedProxyConfig) (proxyConfig, error) {
	if stored.version != 1 {
		return proxyConfig{}, fmt.Errorf("load proxy settings: unsupported version %d", stored.version)
	}
	if stored.keyVersion <= 0 {
		return proxyConfig{}, fmt.Errorf("load proxy settings: unsupported key version %d", stored.keyVersion)
	}
	proxyURL, err := parseProxyURL(stored.proxyURL)
	if err != nil {
		return proxyConfig{}, fmt.Errorf("load proxy settings: %w", err)
	}
	authKey, err := s.decryptAuthKey(stored.keyVersion, stored.nonce, stored.ciphertext)
	if err != nil {
		return proxyConfig{}, fmt.Errorf("load proxy settings: %w", err)
	}
	if stored.enabled && (proxyURL == nil || authKey == "") {
		return proxyConfig{}, errors.New("load proxy settings: enabled proxy requires URL and authentication key")
	}
	return makeProxyConfig(stored.enabled, proxyURL, authKey, SourceDatabase), nil
}

func (s *SettingsService) decryptAuthKey(keyVersion int, nonce, ciphertext []byte) (string, error) {
	if len(nonce) == 0 && len(ciphertext) == 0 {
		return "", nil
	}
	if len(nonce) == 0 || len(ciphertext) == 0 {
		return "", errors.New("proxy authentication data is incomplete")
	}
	plaintext, err := s.keys.DecryptVersion(keyVersion, ciphertext, nonce, proxyPoolAuthKeyAAD)
	if err != nil {
		return "", fmt.Errorf("decrypt proxy authentication key: %w", err)
	}
	defer crypto.Zero(plaintext)
	if strings.TrimSpace(string(plaintext)) == "" {
		return "", errors.New("proxy authentication key is empty")
	}
	return string(plaintext), nil
}

func (s *SettingsService) applyPatch(patch Patch) (proxyConfig, error) {
	current := s.current
	nextEnabled := current.snapshot.Enabled
	if patch.Enabled != nil {
		nextEnabled = *patch.Enabled
	}
	nextURLRaw := current.snapshot.ProxyURL
	if patch.ProxyURL != "" {
		nextURLRaw = strings.TrimSpace(patch.ProxyURL)
	}
	nextAuthKey := current.authKey
	if patch.AuthKey != "" {
		if strings.TrimSpace(patch.AuthKey) == "" {
			return proxyConfig{}, invalidSettings("authentication key cannot be blank")
		}
		nextAuthKey = patch.AuthKey
	}
	if patch.ClearAuthKey {
		if nextEnabled {
			return proxyConfig{}, invalidSettings("clear_auth_key requires proxy to be disabled")
		}
		if patch.AuthKey != "" {
			return proxyConfig{}, invalidSettings("auth_key and clear_auth_key cannot be used together")
		}
		nextAuthKey = ""
	}
	proxyURL, err := parseProxyURL(nextURLRaw)
	if err != nil {
		return proxyConfig{}, err
	}
	if nextEnabled && (proxyURL == nil || strings.TrimSpace(nextAuthKey) == "") {
		return proxyConfig{}, invalidSettings("enabled proxy requires proxy URL and authentication key")
	}
	return makeProxyConfig(nextEnabled, proxyURL, nextAuthKey, SourceDatabase), nil
}

func (s *SettingsService) newManager(config proxyConfig) (*Manager, error) {
	if s.base == nil {
		return nil, errors.New("initialize proxy manager: HTTP transport is required")
	}
	manager, err := New(config.proxyURL, config.authKey, s.base, s.logger)
	if err != nil {
		return nil, fmt.Errorf("initialize proxy manager: %w", err)
	}
	return manager, nil
}

func (s *SettingsService) persist(ctx context.Context, config proxyConfig) error {
	var nonce, ciphertext []byte
	if config.authKey != "" {
		plaintext := []byte(config.authKey)
		defer crypto.Zero(plaintext)
		var err error
		ciphertext, nonce, err = s.keys.Encrypt(plaintext, proxyPoolAuthKeyAAD)
		if err != nil {
			return fmt.Errorf("encrypt proxy authentication key: %w", err)
		}
		defer crypto.Zero(nonce)
		defer crypto.Zero(ciphertext)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxy_pool_settings (
			id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, key_version, updated_at
			) VALUES (1, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			proxy_url = excluded.proxy_url,
			auth_key_nonce = excluded.auth_key_nonce,
			auth_key_ciphertext = excluded.auth_key_ciphertext,
				version = excluded.version,
				key_version = excluded.key_version,
				updated_at = excluded.updated_at`,
		boolInt(config.snapshot.Enabled), config.snapshot.ProxyURL, nonce, ciphertext, s.keys.ActiveVersion(), proxyTimestamp())
	if err != nil {
		return fmt.Errorf("persist proxy settings: %w", err)
	}
	return nil
}

func validateEnvironmentConfig(config EnvironmentConfig) error {
	if config.URL == nil {
		if config.AuthKey != "" {
			return errors.New("proxy authentication key requires proxy URL")
		}
		return nil
	}
	if err := validateProxyURL(config.URL); err != nil {
		return err
	}
	if strings.TrimSpace(config.AuthKey) == "" {
		return errors.New("proxy authentication key is required")
	}
	return nil
}

func cloneEnvironmentConfig(config EnvironmentConfig) EnvironmentConfig {
	clone := config
	if config.URL != nil {
		urlCopy := *config.URL
		clone.URL = &urlCopy
	}
	return clone
}

func environmentProxyConfig(env EnvironmentConfig) (proxyConfig, error) {
	if env.URL == nil {
		return makeProxyConfig(false, nil, "", SourceNone), nil
	}
	return makeProxyConfig(true, env.URL, env.AuthKey, SourceEnvironment), nil
}

func makeProxyConfig(enabled bool, proxyURL *url.URL, authKey string, source Source) proxyConfig {
	proxyURLString := ""
	if proxyURL != nil {
		proxyURLString = proxyURL.String()
	}
	return proxyConfig{
		snapshot: Snapshot{Enabled: enabled, ProxyURL: proxyURLString, AuthConfigured: strings.TrimSpace(authKey) != "", Source: source},
		proxyURL: proxyURL,
		authKey:  authKey,
	}
}

func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, invalidSettings("proxy URL is invalid")
	}
	if err := validateProxyURL(parsed); err != nil {
		return nil, invalidSettings("proxy URL is invalid")
	}
	return parsed, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func proxyTimestamp() string {
	return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
}
