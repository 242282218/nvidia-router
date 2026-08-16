package database

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration025PreservesProxySettingsAndAddsUpstreamSecretColumns(t *testing.T) {
	prior := migrationFixture(t, []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql", "011_streaming_quota.sql",
		"012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql", "014_stream_timeouts.sql",
		"015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql", "017_admin_audit_logs.sql",
		"018_access_key_token_budget.sql", "019_model_pricing.sql",
		"020_latency_routing_and_embedding_cache.sql", "021_provider_credentials.sql",
		"022_deepseek_timeout_backfill.sql", "023_builtin_proxy_pool_safe_settings.sql",
		"024_disable_unsupported_provider_credentials.sql",
	})
	with025 := addMigration(t, prior, "025_proxy_pool_upstream_secret.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-025: %v", err)
	}
	if _, err := db.Exec("INSERT INTO proxy_pool_settings (id, enabled, proxy_url, pool_config, version, key_version, updated_at) VALUES (1, 0, '__built_in_xk_pool__', 'xkcfg:v1:{}', 1, 1, '2026-08-15T00:00:00Z')"); err != nil {
		t.Fatalf("insert legacy pool settings: %v", err)
	}
	if err := migrateFS(db, with025); err != nil {
		t.Fatalf("migrate 025: %v", err)
	}
	var proxyURL, poolConfig, updatedAt string
	var nonce, ciphertext []byte
	if err := db.QueryRow("SELECT proxy_url, pool_config, upstream_url_nonce, upstream_url_ciphertext, updated_at FROM proxy_pool_settings WHERE id = 1").Scan(&proxyURL, &poolConfig, &nonce, &ciphertext, &updatedAt); err != nil {
		t.Fatalf("read migrated settings: %v", err)
	}
	if proxyURL != "__built_in_xk_pool__" || poolConfig != "xkcfg:v1:{}" || updatedAt != "2026-08-15T00:00:00Z" {
		t.Fatalf("migrated settings = url %q pool %q updated %q", proxyURL, poolConfig, updatedAt)
	}
	if len(nonce) != 0 || len(ciphertext) != 0 {
		t.Fatal("new upstream secret columns should be NULL for legacy rows")
	}
}
