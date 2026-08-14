package database

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration024DisablesUnsupportedProvidersWithoutDeletingCredentials(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql", "011_streaming_quota.sql",
		"012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql", "014_stream_timeouts.sql",
		"015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql", "017_admin_audit_logs.sql",
		"018_access_key_token_budget.sql", "019_model_pricing.sql",
		"020_latency_routing_and_embedding_cache.sql", "021_provider_credentials.sql",
		"022_deepseek_timeout_backfill.sql", "023_builtin_proxy_pool_safe_settings.sql",
	}
	prior := migrationFixture(t, priorFiles)
	with024 := addMigration(t, prior, "024_disable_unsupported_provider_credentials.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("open migration fixture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-024: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO provider_credentials (
		name, provider, base_url, ciphertext, nonce, fingerprint, display_prefix, display_suffix,
		enabled, key_version, created_at, updated_at
	) VALUES ('fixture-provider', 'openai_compatible', 'https://api.example.test/v1', x'CAFE', x'BEEF', x'0102', 'fixt', '', 1, 1, '2026-08-15T00:00:00Z', '2026-08-15T00:00:00Z')`); err != nil {
		t.Fatalf("insert provider fixture: %v", err)
	}
	if err := migrateFS(db, with024); err != nil {
		t.Fatalf("migrate 024: %v", err)
	}
	var enabled int
	var ciphertext, nonce []byte
	if err := db.QueryRow("SELECT enabled, ciphertext, nonce FROM provider_credentials WHERE name = 'fixture-provider'").Scan(&enabled, &ciphertext, &nonce); err != nil {
		t.Fatalf("read migrated provider: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("unsupported provider enabled = %d, want 0", enabled)
	}
	if string(ciphertext) != string([]byte{0xCA, 0xFE}) || string(nonce) != string([]byte{0xBE, 0xEF}) {
		t.Fatal("migration deleted or changed encrypted credential material")
	}
	if err := migrateFS(db, with024); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
