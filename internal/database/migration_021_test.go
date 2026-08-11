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

func TestMigration021AddsProviderDimensionAndCredentials(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql", "015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql",
		"017_admin_audit_logs.sql", "018_access_key_token_budget.sql", "019_model_pricing.sql",
		"020_latency_routing_and_embedding_cache.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/021_provider_credentials.sql")
	if err != nil {
		t.Fatalf("read migration 021: %v", err)
	}
	with021 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with021[name] = file
	}
	with021["migrations/021_provider_credentials.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-021: %v", err)
	}
	if err := migrateFS(db, with021); err != nil {
		t.Fatalf("migrate 021: %v", err)
	}

	// Existing models default to the NVIDIA provider.
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, created_at, updated_at)
		VALUES ('existing-model', 'upstream/test', 'Existing', 'chat', 1, '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert model: %v", err)
	}
	var provider string
	if err := db.QueryRow(`SELECT provider FROM models WHERE public_id = 'existing-model'`).Scan(&provider); err != nil {
		t.Fatalf("read model provider: %v", err)
	}
	if provider != "nvidia" {
		t.Fatalf("legacy model provider = %q, want nvidia", provider)
	}

	// A provider credential can be stored with the versioned ciphertext shape.
	if _, err := db.Exec(`
		INSERT INTO provider_credentials (
			name, base_url, ciphertext, nonce, fingerprint,
			display_prefix, display_suffix, key_version, created_at, updated_at
		) VALUES (
			'siliconflow', 'https://api.siliconflow.cn/v1', x'010203', x'0405', x'0607',
			'sk-a', 'xyz1', 1, '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		)
	`); err != nil {
		t.Fatalf("insert provider credential: %v", err)
	}
	var credentialProvider string
	if err := db.QueryRow(`SELECT provider FROM provider_credentials WHERE name = 'siliconflow'`).Scan(&credentialProvider); err != nil {
		t.Fatalf("read credential provider: %v", err)
	}
	if credentialProvider != "openai_compatible" {
		t.Fatalf("credential provider = %q, want openai_compatible", credentialProvider)
	}

	if err := migrateFS(db, with021); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
