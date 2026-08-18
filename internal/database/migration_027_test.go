package database

import (
	"net/url"
	"path/filepath"
	"testing"

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
