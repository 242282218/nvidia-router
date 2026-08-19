package database

import (
	"context"
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration027AddsReasoningProfilesAndAllowsThinkingWireFormat(t *testing.T) {
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
		"024_disable_unsupported_provider_credentials.sql", "025_proxy_pool_upstream_secret.sql",
		"026_reasoning_observability.sql",
	})
	with027 := addMigration(t, prior, "027_reasoning_profiles.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-027: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, supports_reasoning, reasoning_wire_format, created_at, updated_at)
		VALUES ('legacy-profile', 'vendor/legacy-profile', 'Legacy', 'chat', 1, 1, 'openai', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy model: %v", err)
	}
	if err := migrateFS(db, with027); err != nil {
		t.Fatalf("migrate 027: %v", err)
	}
	var levels string
	var minBudget, maxBudget, zeroAllowed, dynamicAllowed int
	if err := db.QueryRow(`SELECT reasoning_levels, reasoning_min_budget, reasoning_max_budget, reasoning_zero_allowed, reasoning_dynamic_allowed FROM models WHERE public_id = 'legacy-profile'`).Scan(&levels, &minBudget, &maxBudget, &zeroAllowed, &dynamicAllowed); err != nil {
		t.Fatalf("read migrated profile: %v", err)
	}
	if levels != `["none","auto","minimal","low","medium","high","xhigh","max"]` || minBudget != 0 || maxBudget != 128000 || zeroAllowed != 1 || dynamicAllowed != 1 {
		t.Fatalf("migrated profile = %q/%d/%d/%d/%d", levels, minBudget, maxBudget, zeroAllowed, dynamicAllowed)
	}
	if _, err := db.Exec(`UPDATE models SET reasoning_wire_format = 'thinking' WHERE public_id = 'legacy-profile'`); err != nil {
		t.Fatalf("thinking wire format rejected: %v", err)
	}
	for _, indexName := range []string{"idx_models_enabled_kind", "idx_key_model_blocks_model"} {
		if !hasIndex(t, db, indexName) {
			t.Fatalf("migration 027 dropped existing index %q", indexName)
		}
	}
}

func TestMigration031AllowsTaskProviderAliasAndPreservesBlocks(t *testing.T) {
	priorNames := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql", "011_streaming_quota.sql",
		"012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql", "014_stream_timeouts.sql",
		"015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql", "017_admin_audit_logs.sql",
		"018_access_key_token_budget.sql", "019_model_pricing.sql", "020_latency_routing_and_embedding_cache.sql",
		"021_provider_credentials.sql", "022_deepseek_timeout_backfill.sql", "023_builtin_proxy_pool_safe_settings.sql",
		"024_disable_unsupported_provider_credentials.sql", "025_proxy_pool_upstream_secret.sql",
		"026_reasoning_observability.sql", "027_reasoning_profiles.sql",
		"028_reasoning_level_observability.sql", "029_opencode_free_provider.sql",
		"030_nvidia_thinking_wire_format.sql",
	}
	files := make(fstest.MapFS, len(priorNames)+1)
	for _, name := range priorNames {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		files["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	migration, err := fs.ReadFile(embeddedMigrations, "migrations/031_opencodefree_provider_alias.sql")
	if err != nil {
		t.Fatalf("read migration 031: %v", err)
	}
	files["migrations/031_opencodefree_provider_alias.sql"] = &fstest.MapFile{Data: migration}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, filesWithout(files, "migrations/031_opencodefree_provider_alias.sql")); err != nil {
		t.Fatalf("migrate prior schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO nvidia_keys (ciphertext, nonce, fingerprint, display_prefix, display_suffix, created_at, updated_at)
		VALUES (X'01', X'02', X'03', 'key', 'tail', '2026-08-19T00:00:00Z', '2026-08-19T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, created_at, updated_at)
		VALUES ('legacy', 'legacy', 'Legacy', 'chat', 1, '2026-08-19T00:00:00Z', '2026-08-19T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, provider, enabled, created_at, updated_at)
		VALUES ('legacy-free', 'free-model', 'Legacy Free Model', 'chat', 'opencode_free', 0, '2026-08-19T00:00:00Z', '2026-08-19T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert legacy OpenCodeFree model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO nvidia_key_model_blocks (nvidia_key_id, model_id, reason_code, first_seen_at, last_seen_at)
		VALUES (1, 1, 'model_forbidden', '2026-08-19T00:00:00Z', '2026-08-19T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert block: %v", err)
	}
	if err := migrateFS(db, files); err != nil {
		t.Fatalf("migrate 031: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, provider, enabled, created_at, updated_at)
		VALUES ('open-code', 'free-model', 'Free Model', 'chat', 'opencodefree', 0, '2026-08-19T00:00:00Z', '2026-08-19T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert OpenCodeFree model: %v", err)
	}
	var provider string
	if err := db.QueryRowContext(context.Background(), "SELECT provider FROM models WHERE public_id = 'open-code'").Scan(&provider); err != nil {
		t.Fatalf("read OpenCodeFree provider: %v", err)
	}
	if provider != "opencodefree" {
		t.Fatalf("provider = %q, want opencodefree", provider)
	}
	var legacyProvider string
	if err := db.QueryRowContext(context.Background(), "SELECT provider FROM models WHERE public_id = 'legacy-free'").Scan(&legacyProvider); err != nil {
		t.Fatalf("read normalized OpenCodeFree provider: %v", err)
	}
	if legacyProvider != "opencodefree" {
		t.Fatalf("normalized provider = %q, want opencodefree", legacyProvider)
	}
	var blockCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM nvidia_key_model_blocks WHERE model_id = 1").Scan(&blockCount); err != nil {
		t.Fatalf("read preserved block: %v", err)
	}
	if blockCount != 1 {
		t.Fatalf("preserved block count = %d, want 1", blockCount)
	}
}

func filesWithout(files fstest.MapFS, excluded string) fstest.MapFS {
	result := make(fstest.MapFS, len(files)-1)
	for name, file := range files {
		if name != excluded {
			result[name] = file
		}
	}
	return result
}
