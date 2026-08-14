package database

import (
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration023PreservesLegacyProxySettings(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql", "011_streaming_quota.sql",
		"012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql", "014_stream_timeouts.sql",
		"015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql", "017_admin_audit_logs.sql",
		"018_access_key_token_budget.sql", "019_model_pricing.sql",
		"020_latency_routing_and_embedding_cache.sql", "021_provider_credentials.sql",
		"022_deepseek_timeout_backfill.sql",
	}
	prior := migrationFixture(t, priorFiles)
	with023 := addMigration(t, prior, "023_builtin_proxy_pool_safe_settings.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-023: %v", err)
	}
	if _, err := db.Exec("INSERT INTO proxy_pool_settings (id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, key_version, updated_at) VALUES (1, 1, 'http://legacy-proxy:8080', x'01', x'02', 1, 1, '2026-08-14T00:00:00Z')"); err != nil {
		t.Fatalf("insert legacy proxy settings: %v", err)
	}
	if err := migrateFS(db, with023); err != nil {
		t.Fatalf("migrate 023: %v", err)
	}
	var enabled, version, keyVersion int
	var proxyURL, updatedAt string
	var nonce, ciphertext []byte
	err = db.QueryRow("SELECT enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, key_version, updated_at FROM proxy_pool_settings WHERE id = 1").Scan(&enabled, &proxyURL, &nonce, &ciphertext, &version, &keyVersion, &updatedAt)
	if err != nil {
		t.Fatalf("read migrated proxy settings: %v", err)
	}
	if enabled != 1 || proxyURL != "http://legacy-proxy:8080" || version != 1 || keyVersion != 1 || updatedAt != "2026-08-14T00:00:00Z" {
		t.Fatalf("migrated settings = enabled %d url %q version %d key_version %d updated_at %q", enabled, proxyURL, version, keyVersion, updatedAt)
	}
	if string(nonce) != string([]byte{1}) || string(ciphertext) != string([]byte{2}) {
		t.Fatalf("encrypted fields were not preserved")
	}
	if _, err := db.Exec("UPDATE proxy_pool_settings SET key_version = 0 WHERE id = 1"); err == nil {
		t.Fatal("migrated proxy settings accepted invalid key_version")
	}
	if err := migrateFS(db, with023); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}

func TestMigration023PreservesLegacyBuiltinSettings(t *testing.T) {
	prior := migrationFixture(t, []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql", "011_streaming_quota.sql",
		"012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql", "014_stream_timeouts.sql",
		"015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql", "017_admin_audit_logs.sql",
		"018_access_key_token_budget.sql", "019_model_pricing.sql",
		"020_latency_routing_and_embedding_cache.sql", "021_provider_credentials.sql",
		"022_deepseek_timeout_backfill.sql",
	})
	with023 := addMigration(t, prior, "023_builtin_proxy_pool_safe_settings.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-023: %v", err)
	}
	if _, err := db.Exec("INSERT INTO proxy_pool_settings (id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, key_version, updated_at) VALUES (1, 1, '__built_in_xk_pool__', x'01', x'02', 1, 1, '2026-08-14T00:00:00Z')"); err != nil {
		t.Fatalf("insert legacy builtin settings: %v", err)
	}
	if err := migrateFS(db, with023); err != nil {
		t.Fatalf("migrate 023: %v", err)
	}
	var enabled int
	var proxyURL string
	if err := db.QueryRow("SELECT enabled, proxy_url FROM proxy_pool_settings WHERE id = 1").Scan(&enabled, &proxyURL); err != nil {
		t.Fatalf("read migrated builtin settings: %v", err)
	}
	if enabled != 1 || proxyURL != "__built_in_xk_pool__" {
		t.Fatalf("migrated builtin settings = enabled %d url %q", enabled, proxyURL)
	}
}

func migrationFixture(t *testing.T, names []string) fstest.MapFS {
	t.Helper()
	files := make(fstest.MapFS, len(names))
	for _, name := range names {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		files["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	return files
}

func addMigration(t *testing.T, prior fstest.MapFS, name string) fstest.MapFS {
	t.Helper()
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	files := make(fstest.MapFS, len(prior)+1)
	for path, file := range prior {
		files[path] = file
	}
	files["migrations/"+name] = &fstest.MapFile{Data: contents}
	return files
}
