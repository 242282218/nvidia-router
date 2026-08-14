package xkproxy

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

func TestSettingsServiceUsesEnvironmentUntilDatabaseOverrideAndEncryptsAuthKey(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keys := testProxyKeySet(t, 1)
	proxyURL, err := url.Parse("http://proxy-pool:8080")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	service, err := NewSettingsService(context.Background(), db, keys, EnvironmentConfig{URL: proxyURL, AuthKey: "proxy-secret"}, nil, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)

	initial, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("initial Snapshot: %v", err)
	}
	if initial.Source != SourceEnvironment || !initial.Enabled || !initial.AuthConfigured || initial.ProxyURL != proxyURL.String() {
		t.Fatalf("initial snapshot = %#v", initial)
	}

	disabled := false
	updated, err := service.Update(context.Background(), Patch{Enabled: &disabled})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Source != SourceDatabase || updated.Enabled || !updated.AuthConfigured {
		t.Fatalf("updated snapshot = %#v", updated)
	}
	var ciphertext []byte
	if err := db.QueryRow("SELECT auth_key_ciphertext FROM proxy_pool_settings WHERE id = 1").Scan(&ciphertext); err != nil {
		t.Fatalf("read encrypted auth key: %v", err)
	}
	if strings.Contains(string(ciphertext), "proxy-secret") {
		t.Fatal("proxy auth key was stored in plaintext")
	}

	if _, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 2), EnvironmentConfig{}, nil, http.DefaultTransport.(*http.Transport), discardProxyLogger()); err == nil {
		t.Fatal("wrong master key loaded proxy settings")
	}
	cleared, err := service.Update(context.Background(), Patch{ClearAuthKey: true})
	if err != nil {
		t.Fatalf("clear auth key: %v", err)
	}
	if cleared.AuthConfigured {
		t.Fatal("cleared auth key remains configured")
	}
}

func TestSettingsServiceRejectsEnableWithoutProxyCredentials(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, nil, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)

	enabled := true
	_, err = service.Update(context.Background(), Patch{Enabled: &enabled})
	if err == nil || !strings.Contains(err.Error(), "proxy URL") {
		t.Fatalf("Update error = %v, want proxy URL validation", err)
	}
}

func testProxyKeySet(t *testing.T, firstByte byte) *crypto.KeySet {
	t.Helper()
	var master [32]byte
	master[0] = firstByte
	keys, err := crypto.New(master)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return keys
}

func TestSettingsServicePoolModeBuildsCollectorManager(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	poolCfg := &CollectorConfig{
		UpstreamURL:      "https://api.xingkongdaili.com/api/getproxy/123",
		UpstreamTimeout:  time.Second,
		ValidationURL:    "https://integrate.api.nvidia.com/v1",
		ValidationStatus: 404,
		Interval:         10 * time.Second, // never actually fires in the test window
		ProxyTTL:         time.Minute,
		ExpectedQty:      2,
		Concurrency:      2,
	}
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{AuthKey: "pool-secret"}, poolCfg, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)

	initial, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !initial.Enabled || initial.Mode != "built-in" || initial.ProxyURL != "" {
		t.Fatalf("pool mode snapshot = %#v", initial)
	}
	if initial.Source != SourceEnvironment {
		t.Fatalf("Source = %q, want environment", initial.Source)
	}
	if !initial.AuthConfigured {
		t.Fatal("pool auth key should be configured")
	}
	switcher := service.Switcher()
	if switcher == nil {
		t.Fatal("Switcher() = nil")
	}
	if !switcher.Configured() {
		t.Fatal("pool mode switcher should be configured")
	}
}

func TestSettingsServicePoolModeRejectsFixedProxyMutation(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	poolCfg := &CollectorConfig{
		UpstreamURL:   "https://api.xingkongdaili.com/tools/XApi.ashx?apikey=fixture",
		ValidationURL: "https://integrate.api.nvidia.com/v1", ValidationStatus: 404,
		UpstreamTimeout: time.Second, ValidationTimeout: time.Second,
		Interval: 10 * time.Second, ProxyTTL: time.Minute, ExpectedQty: 2, Concurrency: 1,
	}
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, poolCfg, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)
	enabled := true
	_, err = service.Update(context.Background(), Patch{Enabled: &enabled, ProxyURL: "http://other:8080"})
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("Update error = %v, want built-in mode validation", err)
	}
}

func TestSettingsServicePoolModePersistsSafeSettingsAndUsesRuntimeXAPI(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	initial := &CollectorConfig{
		UpstreamURL:   "https://old.example.test/XApi?secret=fixture",
		ValidationURL: "https://integrate.api.nvidia.com/v1", ValidationStatus: 404,
		UpstreamTimeout: time.Second, ValidationTimeout: time.Second,
		Interval: 10 * time.Second, ProxyTTL: time.Minute, ExpectedQty: 2, Concurrency: 1,
	}
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, initial, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)

	updated, err := service.Update(context.Background(), Patch{UpstreamURL: stringPointer("https://new.example.test/XApi?secret=fixture"), Interval: stringPointer("10s")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.Enabled || updated.Mode != "built-in" || !updated.UpstreamConfigured || updated.UpstreamEndpoint != "https://new.example.test/XApi" {
		t.Fatalf("updated snapshot = %#v", updated)
	}
	var ciphertext []byte
	var poolConfig string
	if err := db.QueryRow("SELECT auth_key_ciphertext, pool_config FROM proxy_pool_settings WHERE id = 1").Scan(&ciphertext, &poolConfig); err != nil {
		t.Fatalf("read persisted pool settings: %v", err)
	}
	if len(ciphertext) != 0 {
		t.Fatal("built-in pool persisted encrypted credential data")
	}
	if strings.Contains(poolConfig, "secret=") || strings.Contains(poolConfig, "old.example") || strings.Contains(poolConfig, "new.example") {
		t.Fatalf("pool_config contains sensitive upstream data: %q", poolConfig)
	}

	reloaded, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, initial, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	t.Cleanup(reloaded.Close)
	snapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reloaded Snapshot: %v", err)
	}
	if snapshot.UpstreamEndpoint != "https://old.example.test/XApi" || !snapshot.UpstreamConfigured {
		t.Fatalf("reloaded snapshot = %#v", snapshot)
	}
}

func TestSettingsServicePoolPatchRejectsInvalidValues(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, &CollectorConfig{
		UpstreamURL: "https://api.example.test/XApi?apikey=fixture", ValidationURL: "https://validate.example.test",
		ValidationStatus: 200, UpstreamTimeout: time.Second, ValidationTimeout: time.Second,
		Interval: 10 * time.Second, ProxyTTL: 20 * time.Second, ExpectedQty: 2, Concurrency: 2,
	}, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)
	for _, test := range []struct {
		name  string
		patch Patch
	}{
		{name: "status below range", patch: Patch{ValidationStatus: intPointer(99)}},
		{name: "status above range", patch: Patch{ValidationStatus: intPointer(600)}},
		{name: "zero expected quantity", patch: Patch{ExpectedQty: intPointer(0)}},
		{name: "negative expected quantity", patch: Patch{ExpectedQty: intPointer(-1)}},
		{name: "zero concurrency", patch: Patch{Concurrency: intPointer(0)}},
		{name: "negative concurrency", patch: Patch{Concurrency: intPointer(-1)}},
		{name: "interval below minimum", patch: Patch{Interval: stringPointer("4s")}},
		{name: "ttl above maximum", patch: Patch{ProxyTTL: stringPointer("181s")}},
		{name: "ttl not longer than interval", patch: Patch{Interval: stringPointer("15s"), ProxyTTL: stringPointer("15s")}},
		{name: "ttl shorter than interval", patch: Patch{Interval: stringPointer("15s"), ProxyTTL: stringPointer("10s")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Update(context.Background(), test.patch); err == nil {
				t.Fatal("Update accepted invalid pool settings")
			}
		})
	}
}

func TestSettingsServicePoolPatchClearsMaxLatency(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, &CollectorConfig{
		UpstreamURL: "https://api.example.test/XApi?apikey=fixture", ValidationURL: "https://validate.example.test",
		ValidationStatus: 200, UpstreamTimeout: time.Second, ValidationTimeout: time.Second,
		MaxLatency: 3 * time.Second, Interval: 10 * time.Second, ProxyTTL: 20 * time.Second, ExpectedQty: 2, Concurrency: 2,
	}, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)
	if _, err := service.Update(context.Background(), Patch{MaxLatency: stringPointer("")}); err != nil {
		t.Fatalf("clear MaxLatency: %v", err)
	}
	var poolConfig string
	if err := db.QueryRow("SELECT pool_config FROM proxy_pool_settings WHERE id = 1").Scan(&poolConfig); err != nil {
		t.Fatalf("read pool config: %v", err)
	}
	if strings.Contains(poolConfig, "max_latency") {
		t.Fatalf("cleared MaxLatency persisted: %s", poolConfig)
	}
}

func TestSettingsServiceSavingLoadedSnapshotPreservesPoolConfig(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	initial := &CollectorConfig{
		UpstreamURL: "https://api.example.test/XApi?apikey=fixture", ValidationURL: "https://validate.example.test/health",
		ValidationStatus: 204, UpstreamTimeout: time.Second, ValidationTimeout: time.Second, MaxLatency: 2 * time.Second,
		Interval: 10 * time.Second, ProxyTTL: 30 * time.Second, ExpectedQty: 2, Concurrency: 3,
	}
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, initial, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("initial Snapshot: %v", err)
	}
	if _, err := service.Update(context.Background(), patchFromSnapshot(snapshot)); err != nil {
		t.Fatalf("initial full save: %v", err)
	}
	service.Close()

	before := readPoolConfigRow(t, db)
	reloaded, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, initial, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	t.Cleanup(reloaded.Close)
	reloadedSnapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reloaded Snapshot: %v", err)
	}
	if _, err := reloaded.Update(context.Background(), patchFromSnapshot(reloadedSnapshot)); err != nil {
		t.Fatalf("reloaded full save: %v", err)
	}
	after := readPoolConfigRow(t, db)
	if before != after {
		t.Fatalf("full snapshot save changed persisted config: before=%s after=%s", before, after)
	}
}

func patchFromSnapshot(snapshot Snapshot) Patch {
	return Patch{
		ValidationURL:    stringPointer(snapshot.ValidationURL),
		ValidationStatus: intPointer(snapshot.ValidationStatus),
		Interval:         stringPointer(snapshot.CollectorInterval),
		ProxyTTL:         stringPointer(snapshot.ProxyTTL),
		ExpectedQty:      intPointer(snapshot.ExpectedQty),
		Concurrency:      intPointer(snapshot.Concurrency),
		MaxLatency:       stringPointer(snapshot.MaxLatency),
	}
}

func readPoolConfigRow(t *testing.T, db *sql.DB) string {
	t.Helper()
	var row string
	if err := db.QueryRow("SELECT printf('%d|%s|%s|%s|%s|%d', enabled, proxy_url, pool_config, hex(auth_key_nonce), hex(auth_key_ciphertext), version) FROM proxy_pool_settings WHERE id = 1").Scan(&row); err != nil {
		t.Fatalf("read persisted pool row: %v", err)
	}
	return row
}

func TestUpstreamClientFetchUsesConfiguredExpectedQty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("qty"); got != "7" {
			t.Errorf("upstream qty = %q, want 7", got)
		}
		_, _ = writer.Write([]byte("203.0.113.10:8080"))
	}))
	t.Cleanup(server.Close)
	client := NewUpstreamClient(server.URL+"?apikey=fixture", time.Second, 7)
	t.Cleanup(client.Close)
	if _, _, err := client.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestSettingsServicePoolModeRequiresRuntimeXAPIWhenEnabled(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	poolCfg := &CollectorConfig{
		UpstreamURL: "https://provider.example/XApi?apikey=fixture", ValidationURL: "https://integrate.api.nvidia.com/v1",
		ValidationStatus: 404, UpstreamTimeout: time.Second, ValidationTimeout: time.Second,
		Interval: 10 * time.Second, ProxyTTL: time.Minute, ExpectedQty: 2, Concurrency: 1,
	}
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, poolCfg, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	enabled := true
	if _, err := service.Update(context.Background(), Patch{Enabled: &enabled}); err != nil {
		t.Fatalf("persist enabled pool: %v", err)
	}
	service.Close()

	if _, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, nil, http.DefaultTransport.(*http.Transport), discardProxyLogger()); err == nil || !strings.Contains(err.Error(), "runtime XApi") {
		t.Fatalf("reload without runtime XApi error = %v", err)
	}
}

func TestSettingsServiceRejectsLegacyExternalDatabaseConfiguration(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		INSERT INTO proxy_pool_settings (
			id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, key_version, updated_at
		) VALUES (1, 1, 'http://legacy-proxy:8080', x'01', x'02', 1, 1, '2026-08-14T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert legacy settings: %v", err)
	}
	if _, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, nil, http.DefaultTransport.(*http.Transport), discardProxyLogger()); err == nil || !strings.Contains(err.Error(), "external proxy settings are unsupported") {
		t.Fatalf("legacy external settings error = %v", err)
	}
}

func TestSettingsServiceRejectsNewExternalProxyConfiguration(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewSettingsService(context.Background(), db, testProxyKeySet(t, 1), EnvironmentConfig{}, nil, http.DefaultTransport.(*http.Transport), discardProxyLogger())
	if err != nil {
		t.Fatalf("NewSettingsService: %v", err)
	}
	t.Cleanup(service.Close)
	enabled := true
	_, err = service.Update(context.Background(), Patch{Enabled: &enabled, ProxyURL: "http://legacy-proxy:8080", AuthKey: "runtime-only"})
	if err == nil || !strings.Contains(err.Error(), "external proxy settings are unsupported") {
		t.Fatalf("external configuration error = %v", err)
	}
}

func discardProxyLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func stringPointer(value string) *string { return &value }
func intPointer(value int) *int          { return &value }
