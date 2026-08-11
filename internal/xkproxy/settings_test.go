package xkproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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
		Interval:         time.Hour, // never actually fires in the test window
		ProxyTTL:         time.Minute,
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
	if !initial.Enabled {
		t.Fatal("pool mode should be enabled by default")
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

func discardProxyLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
