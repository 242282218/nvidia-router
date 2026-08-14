package xkproxy

import (
	"context"
	"database/sql"
	"encoding/json"
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
const proxyPoolConfigPrefix = "xkcfg:v1:"
const builtInProxyURLSentinel = "__built_in_xk_pool__"

type Source string

const (
	SourceDatabase    Source = "database"
	SourceEnvironment Source = "environment"
	SourceNone        Source = "none"
)

type Snapshot struct {
	Enabled            bool   `json:"enabled"`
	ProxyURL           string `json:"proxy_url"`
	AuthConfigured     bool   `json:"auth_configured"`
	Source             Source `json:"source"`
	Mode               string `json:"mode,omitempty"`
	UpstreamConfigured bool   `json:"upstream_configured,omitempty"`
	UpstreamEndpoint   string `json:"upstream_endpoint,omitempty"`
	CollectorInterval  string `json:"collector_interval,omitempty"`
	ProxyTTL           string `json:"proxy_ttl,omitempty"`
	ValidationURL      string `json:"validation_url,omitempty"`
	ValidationStatus   int    `json:"validation_status,omitempty"`
	ExpectedQty        int    `json:"expected_qty,omitempty"`
	Concurrency        int    `json:"concurrency,omitempty"`
	MaxLatency         string `json:"max_latency,omitempty"`
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
	Enabled          *bool   `json:"enabled"`
	ProxyURL         string  `json:"proxy_url"`
	AuthKey          string  `json:"auth_key"`
	ClearAuthKey     bool    `json:"clear_auth_key"`
	UpstreamURL      *string `json:"upstream_url"`
	ValidationURL    *string `json:"validation_url"`
	ValidationStatus *int    `json:"validation_status"`
	Interval         *string `json:"interval"`
	ProxyTTL         *string `json:"proxy_ttl"`
	ExpectedQty      *int    `json:"expected_qty"`
	Concurrency      *int    `json:"concurrency"`
	MaxLatency       *string `json:"max_latency"`
}

type SettingsService struct {
	db       *sql.DB
	keys     *crypto.KeySet
	env      EnvironmentConfig
	poolCfg  *CollectorConfig // built-in proxy pool (collector mode), nil for static proxy
	base     *http.Transport
	logger   *slog.Logger
	switcher *Switcher

	mu      sync.Mutex
	current proxyConfig
	closed  bool
	// runCtx is cancelled by Close so collectors started from newManager stop
	// together with the service. It is created on construction because app wiring
	// creates the service before the app's root context exists.
	runCtx    context.Context
	runCancel context.CancelFunc
}

type proxyConfig struct {
	snapshot Snapshot
	proxyURL *url.URL
	authKey  string
	poolCfg  *CollectorConfig
}

type persistedPoolConfig struct {
	ValidationURL     string `json:"validation_url"`
	ValidationStatus  int    `json:"validation_status"`
	UpstreamTimeout   string `json:"upstream_timeout"`
	ValidationTimeout string `json:"validation_timeout"`
	MaxLatency        string `json:"max_latency,omitempty"`
	Interval          string `json:"interval"`
	ProxyTTL          string `json:"proxy_ttl"`
	ExpectedQty       int    `json:"expected_qty"`
	Concurrency       int    `json:"concurrency"`
}

type storedProxyConfig struct {
	enabled    bool
	proxyURL   string
	poolConfig string
	nonce      []byte
	ciphertext []byte
	version    int
	keyVersion int
}

func NewSettingsService(ctx context.Context, db *sql.DB, keys *crypto.KeySet, env EnvironmentConfig, poolCfg *CollectorConfig, base *http.Transport, logger *slog.Logger) (*SettingsService, error) {
	if db == nil {
		return nil, errors.New("initialize proxy settings: database is required")
	}
	if keys == nil {
		return nil, errors.New("initialize proxy settings: crypto keys are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	service := &SettingsService{db: db, keys: keys, env: cloneEnvironmentConfig(env), poolCfg: poolCfg, base: base, logger: logger, runCtx: runCtx, runCancel: runCancel}
	if err := service.validateEnvironment(); err != nil {
		runCancel()
		return nil, err
	}
	config, err := service.loadCurrent(ctx)
	if err != nil {
		runCancel()
		return nil, err
	}
	var manager *Manager
	if config.snapshot.Enabled {
		manager, err = service.newManager(config)
		if err != nil {
			runCancel()
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

// PoolStatus returns the live built-in proxy-pool state (counts, per-proxy
// quality) for the admin UI. Static-proxy mode returns an empty status.
func (s *SettingsService) PoolStatus() PoolStatus {
	if s == nil || s.switcher == nil {
		return PoolStatus{}
	}
	return s.switcher.PoolStatus()
}

func (s *SettingsService) Refresh(ctx context.Context) error {
	if s == nil || s.switcher == nil {
		return errors.New("proxy settings service is nil")
	}
	return s.switcher.Refresh(ctx)
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
	if s.runCancel != nil {
		s.runCancel()
	}
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
		return s.environmentProxyConfig()
	}
	return s.databaseProxyConfig(*stored)
}

func (s *SettingsService) loadStored(ctx context.Context) (*storedProxyConfig, error) {
	var enabled int
	var stored storedProxyConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, proxy_url, pool_config, auth_key_nonce, auth_key_ciphertext, version, key_version
		FROM proxy_pool_settings WHERE id = 1`).Scan(
		&enabled, &stored.proxyURL, &stored.poolConfig, &stored.nonce, &stored.ciphertext, &stored.version, &stored.keyVersion,
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
	if s.poolCfg == nil && stored.proxyURL != "" && stored.proxyURL != builtInProxyURLSentinel {
		return proxyConfig{}, errors.New("load proxy settings: external proxy settings are unsupported; configure the built-in XApi pool")
	}
	if s.poolCfg != nil || stored.proxyURL == builtInProxyURLSentinel {
		if stored.proxyURL != "" && stored.proxyURL != builtInProxyURLSentinel {
			return proxyConfig{}, errors.New("load proxy settings: built-in mode contains an invalid fixed proxy URL")
		}
		if s.poolCfg == nil {
			if stored.enabled {
				return proxyConfig{}, errors.New("load proxy settings: enabled built-in pool requires runtime XApi configuration")
			}
			return makeProxyConfig(false, nil, "", SourceDatabase), nil
		}
		config := makeProxyConfig(stored.enabled, nil, "", SourceDatabase)
		config.poolCfg = cloneCollectorConfig(s.poolCfg)
		if len(stored.ciphertext) > 0 {
			return proxyConfig{}, errors.New("load proxy settings: built-in pool contains persisted credential data")
		}
		if stored.poolConfig != "" {
			poolConfig, err := collectorConfigFromPersisted(stored.poolConfig, s.poolCfg.UpstreamURL)
			if err != nil {
				return proxyConfig{}, fmt.Errorf("load proxy settings: %w", err)
			}
			config.poolCfg = poolConfig
		}
		return applyPoolSnapshot(config), nil
	}
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
	if stored.enabled && s.poolCfg == nil && (proxyURL == nil || authKey == "") {
		return proxyConfig{}, errors.New("load proxy settings: enabled proxy requires URL and authentication key")
	}
	config := makeProxyConfig(stored.enabled, proxyURL, authKey, SourceDatabase)
	if s.poolCfg != nil {
		config.snapshot.Mode = "built-in"
		config.snapshot.AuthConfigured = true
	}
	return config, nil
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
	if strings.TrimSpace(patch.ProxyURL) != "" || strings.TrimSpace(patch.AuthKey) != "" {
		return proxyConfig{}, invalidSettings("external proxy settings are unsupported; configure the built-in XApi pool")
	}
	current := s.current
	nextEnabled := current.snapshot.Enabled
	if patch.Enabled != nil {
		nextEnabled = *patch.Enabled
	}
	if s.poolCfg != nil || current.poolCfg != nil {
		if strings.TrimSpace(patch.ProxyURL) != "" || strings.TrimSpace(patch.AuthKey) != "" || patch.ClearAuthKey {
			return proxyConfig{}, invalidSettings("built-in mode does not accept fixed proxy credentials")
		}
		poolConfig := cloneCollectorConfig(current.poolCfg)
		if poolConfig == nil {
			poolConfig = cloneCollectorConfig(s.poolCfg)
		}
		if err := applyPoolPatch(poolConfig, patch); err != nil {
			return proxyConfig{}, err
		}
		config := makeProxyConfig(nextEnabled, nil, "", SourceDatabase)
		config.poolCfg = poolConfig
		return applyPoolSnapshot(config), nil
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
	if config.poolCfg != nil && config.proxyURL == nil {
		// Built-in pool mode: fetch and validate proxy addresses from the
		// upstream API. The auth key authenticates the router to the pool's
		// provider (static-manager compatibility), not a single fixed proxy.
		poolConfig := *config.poolCfg
		poolConfig.UpstreamURL = upstreamURLWithQuantity(poolConfig.UpstreamURL, poolConfig.ExpectedQty)
		manager, err := NewWithPool(poolConfig, config.authKey, s.base, s.logger)
		if err != nil {
			return nil, fmt.Errorf("initialize proxy pool manager: %w", err)
		}
		manager.StartCollector(s.runCtx)
		return manager, nil
	}
	manager, err := New(config.proxyURL, config.authKey, s.base, s.logger)
	if err != nil {
		return nil, fmt.Errorf("initialize proxy manager: %w", err)
	}
	return manager, nil
}

func (s *SettingsService) persist(ctx context.Context, config proxyConfig) error {
	var nonce, ciphertext []byte
	poolConfig := ""
	if config.poolCfg != nil {
		encoded, err := marshalPoolConfig(*config.poolCfg)
		if err != nil {
			return err
		}
		poolConfig = encoded
	} else if config.authKey != "" {
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
	proxyURL := config.snapshot.ProxyURL
	if config.poolCfg != nil && config.snapshot.Enabled {
		proxyURL = builtInProxyURLSentinel
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxy_pool_settings (
			id, enabled, proxy_url, pool_config, auth_key_nonce, auth_key_ciphertext, version, key_version, updated_at
			) VALUES (1, ?, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			proxy_url = excluded.proxy_url,
			auth_key_nonce = excluded.auth_key_nonce,
			auth_key_ciphertext = excluded.auth_key_ciphertext,
				pool_config = excluded.pool_config,
				version = excluded.version,
				key_version = excluded.key_version,
				updated_at = excluded.updated_at`,
		boolInt(config.snapshot.Enabled), proxyURL, poolConfig, nonce, ciphertext, s.keys.ActiveVersion(), proxyTimestamp())
	if err != nil {
		return fmt.Errorf("persist proxy settings: %w", err)
	}
	return nil
}

func (s *SettingsService) validateEnvironment() error {
	if s.poolCfg != nil {
		// Built-in pool mode: the auth key authenticates the router to the proxy
		// provider (upstream API), not to a fixed proxy URL, so a key without a
		// URL is valid. The pool configuration itself carries the URL contract.
		return validatePoolConfig(*s.poolCfg)
	}
	return validateEnvironmentConfig(s.env)
}

func validatePoolConfig(cfg CollectorConfig) error {
	if strings.TrimSpace(cfg.UpstreamURL) == "" {
		return errors.New("proxy pool: upstream URL is required")
	}
	if strings.TrimSpace(cfg.ValidationURL) == "" {
		return errors.New("proxy pool: validation URL is required")
	}
	if cfg.ValidationStatus < 100 || cfg.ValidationStatus > 599 {
		return errors.New("proxy pool: validation status must be between 100 and 599")
	}
	if cfg.ExpectedQty != 2 {
		return errors.New("proxy pool: expected quantity must be exactly 2")
	}
	if cfg.Concurrency <= 0 {
		return errors.New("proxy pool: concurrency must be positive")
	}
	if cfg.Interval < 5*time.Second {
		return errors.New("proxy pool: collect interval must be at least 5s")
	}
	if cfg.ProxyTTL < time.Second || cfg.ProxyTTL > 180*time.Second {
		return errors.New("proxy pool: proxy TTL must be between 1s and 180s")
	}
	if cfg.ProxyTTL <= cfg.Interval {
		return errors.New("proxy pool: proxy TTL must be longer than collect interval")
	}
	if cfg.UpstreamTimeout > 0 && cfg.UpstreamTimeout >= cfg.Interval {
		return errors.New("proxy pool: upstream timeout must be less than collect interval")
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

func (s *SettingsService) environmentProxyConfig() (proxyConfig, error) {
	if s.poolCfg != nil {
		// Built-in pool mode: the router fetches and validates proxy addresses
		// from the upstream API configured in poolCfg. There is no fixed proxy
		// URL, so proxyURL is nil and newManager routes to NewWithPool. The auth
		// key (pool provider credential) comes from the static env override.
		config := makeProxyConfig(true, nil, "", SourceEnvironment)
		config.poolCfg = cloneCollectorConfig(s.poolCfg)
		return applyPoolSnapshot(config), nil
	}
	if s.env.URL == nil {
		return makeProxyConfig(false, nil, "", SourceNone), nil
	}
	return makeProxyConfig(true, s.env.URL, s.env.AuthKey, SourceEnvironment), nil
}

func makeProxyConfig(enabled bool, proxyURL *url.URL, authKey string, source Source) proxyConfig {
	proxyURLString := ""
	if proxyURL != nil {
		proxyURLString = proxyURL.String()
	}
	mode := "none"
	if enabled && proxyURL == nil {
		mode = "built-in"
	} else if enabled {
		mode = "external"
	}
	return proxyConfig{
		snapshot: Snapshot{Enabled: enabled, ProxyURL: proxyURLString, AuthConfigured: strings.TrimSpace(authKey) != "", Source: source, Mode: mode},
		proxyURL: proxyURL,
		authKey:  authKey,
	}
}

func applyPoolSnapshot(config proxyConfig) proxyConfig {
	config.snapshot.Mode = "built-in"
	config.snapshot.AuthConfigured = config.poolCfg != nil && strings.TrimSpace(config.poolCfg.UpstreamURL) != ""
	if config.poolCfg != nil {
		config.snapshot.UpstreamConfigured = config.snapshot.AuthConfigured
		config.snapshot.UpstreamEndpoint = safeEndpoint(config.poolCfg.UpstreamURL)
		config.snapshot.CollectorInterval = config.poolCfg.Interval.String()
		config.snapshot.ProxyTTL = config.poolCfg.ProxyTTL.String()
		config.snapshot.ValidationURL = config.poolCfg.ValidationURL
		config.snapshot.ValidationStatus = config.poolCfg.ValidationStatus
		config.snapshot.ExpectedQty = config.poolCfg.ExpectedQty
		config.snapshot.Concurrency = config.poolCfg.Concurrency
		if config.poolCfg.MaxLatency > 0 {
			config.snapshot.MaxLatency = config.poolCfg.MaxLatency.String()
		}
	}
	return config
}

func safeEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func cloneCollectorConfig(value *CollectorConfig) *CollectorConfig {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func marshalPoolConfig(config CollectorConfig) (string, error) {
	persisted := persistedPoolConfig{ValidationURL: config.ValidationURL, ValidationStatus: config.ValidationStatus, UpstreamTimeout: config.UpstreamTimeout.String(), ValidationTimeout: config.ValidationTimeout.String(), Interval: config.Interval.String(), ProxyTTL: config.ProxyTTL.String(), ExpectedQty: config.ExpectedQty, Concurrency: config.Concurrency}
	if config.MaxLatency > 0 {
		persisted.MaxLatency = config.MaxLatency.String()
	}
	data, err := json.Marshal(persisted)
	if err != nil {
		return "", fmt.Errorf("marshal proxy pool config: %w", err)
	}
	return proxyPoolConfigPrefix + string(data), nil
}

func collectorConfigFromPersisted(value, upstreamURL string) (*CollectorConfig, error) {
	var persisted persistedPoolConfig
	if err := json.Unmarshal([]byte(strings.TrimPrefix(value, proxyPoolConfigPrefix)), &persisted); err != nil {
		return nil, fmt.Errorf("decode proxy pool config: %w", err)
	}
	return collectorConfigFromPersistedValues(persisted, upstreamURL)
}

func collectorConfigFromPersistedValues(value persistedPoolConfig, upstreamURL string) (*CollectorConfig, error) {
	upstreamTimeout, err := time.ParseDuration(value.UpstreamTimeout)
	if err != nil {
		return nil, invalidSettings("upstream timeout is invalid")
	}
	validationTimeout, err := time.ParseDuration(value.ValidationTimeout)
	if err != nil {
		return nil, invalidSettings("validation timeout is invalid")
	}
	interval, err := time.ParseDuration(value.Interval)
	if err != nil {
		return nil, invalidSettings("collect interval is invalid")
	}
	proxyTTL, err := time.ParseDuration(value.ProxyTTL)
	if err != nil {
		return nil, invalidSettings("proxy TTL is invalid")
	}
	maxLatency := time.Duration(0)
	if value.MaxLatency != "" {
		maxLatency, err = time.ParseDuration(value.MaxLatency)
		if err != nil {
			return nil, invalidSettings("max latency is invalid")
		}
	}
	config := &CollectorConfig{UpstreamURL: upstreamURL, ValidationURL: value.ValidationURL, ValidationStatus: value.ValidationStatus, UpstreamTimeout: upstreamTimeout, ValidationTimeout: validationTimeout, MaxLatency: maxLatency, Interval: interval, ProxyTTL: proxyTTL, ExpectedQty: value.ExpectedQty, Concurrency: value.Concurrency}
	if err := validatePoolConfig(*config); err != nil {
		return nil, invalidSettings(err.Error())
	}
	return config, nil
}

func applyPoolPatch(config *CollectorConfig, patch Patch) error {
	if config == nil {
		return errors.New("proxy pool config is missing")
	}
	if patch.UpstreamURL != nil {
		config.UpstreamURL = strings.TrimSpace(*patch.UpstreamURL)
	}
	if patch.ValidationURL != nil {
		config.ValidationURL = strings.TrimSpace(*patch.ValidationURL)
	}
	if patch.ValidationStatus != nil {
		config.ValidationStatus = *patch.ValidationStatus
	}
	if patch.ExpectedQty != nil {
		config.ExpectedQty = *patch.ExpectedQty
	}
	if patch.Concurrency != nil {
		config.Concurrency = *patch.Concurrency
	}
	var err error
	if patch.Interval != nil {
		config.Interval, err = time.ParseDuration(strings.TrimSpace(*patch.Interval))
		if err != nil {
			return invalidSettings("collect interval is invalid")
		}
	}
	if patch.ProxyTTL != nil {
		config.ProxyTTL, err = time.ParseDuration(strings.TrimSpace(*patch.ProxyTTL))
		if err != nil {
			return invalidSettings("proxy TTL is invalid")
		}
	}
	if patch.MaxLatency != nil {
		if strings.TrimSpace(*patch.MaxLatency) == "" {
			config.MaxLatency = 0
		} else {
			config.MaxLatency, err = time.ParseDuration(strings.TrimSpace(*patch.MaxLatency))
			if err != nil || config.MaxLatency <= 0 {
				return invalidSettings("max latency is invalid")
			}
		}
	}
	return validatePoolConfig(*config)
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
