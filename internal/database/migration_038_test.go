package database

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration038BackfillsObservedNVIDIAReasoningModels(t *testing.T) {
	prior := migrationFixture(t, []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql", "011_streaming_quota.sql",
		"012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql", "014_stream_timeouts.sql",
		"015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql", "017_admin_audit_logs.sql",
		"018_access_key_token_budget.sql", "019_model_pricing.sql", "020_latency_routing_and_embedding_cache.sql",
		"021_provider_credentials.sql", "022_deepseek_timeout_backfill.sql", "023_builtin_proxy_pool_safe_settings.sql",
		"024_disable_unsupported_provider_credentials.sql", "025_proxy_pool_upstream_secret.sql",
		"026_reasoning_observability.sql", "027_reasoning_profiles.sql", "028_reasoning_level_observability.sql",
		"029_opencode_free_provider.sql", "030_nvidia_thinking_wire_format.sql",
		"031_opencodefree_provider_alias.sql", "032_monitoring_query_indexes.sql",
		"033_canceled_outcome.sql", "034_slow_reasoning_timeouts.sql", "035_perf_indexes.sql",
		"036_perf_indexes_v2.sql", "037_model_health.sql",
	})
	with038 := addMigration(t, prior, "038_nvidia_reasoning_capability_backfill.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-038: %v", err)
	}
	for _, row := range []struct {
		publicID, upstreamID, provider string
	}{
		{"nemotron", "nvidia/nemotron-3-ultra-550b-a55b", "nvidia"},
		{"step", "stepfun-ai/step-3.7-flash", "nvidia"},
		{"free-step", "stepfun-ai/step-3.7-flash", "opencodefree"},
	} {
		if _, err := db.Exec(`INSERT INTO models (public_id, upstream_id, display_name, kind, provider, enabled, supports_reasoning, reasoning_wire_format, reasoning_levels, reasoning_zero_allowed, reasoning_dynamic_allowed, created_at, updated_at)
			VALUES (?, ?, ?, 'chat', ?, 1, 0, 'none', '["none"]', 0, 0, '2026-08-20T00:00:00Z', '2026-08-20T00:00:00Z')`, row.publicID, row.upstreamID, row.publicID, row.provider); err != nil {
			t.Fatalf("insert %s: %v", row.publicID, err)
		}
	}
	if err := migrateFS(db, with038); err != nil {
		t.Fatalf("migrate 038: %v", err)
	}

	for _, publicID := range []string{"nemotron", "step"} {
		var supports, zeroAllowed, dynamicAllowed int
		var wire, levels string
		if err := db.QueryRow(`SELECT supports_reasoning, reasoning_wire_format, reasoning_levels, reasoning_zero_allowed, reasoning_dynamic_allowed FROM models WHERE public_id = ?`, publicID).Scan(&supports, &wire, &levels, &zeroAllowed, &dynamicAllowed); err != nil {
			t.Fatalf("read %s: %v", publicID, err)
		}
		if supports != 1 || wire != "thinking" || levels != `["none","auto","minimal","low","medium","high","xhigh","max"]` || zeroAllowed != 1 || dynamicAllowed != 1 {
			t.Fatalf("%s metadata = %d/%q/%q/%d/%d", publicID, supports, wire, levels, zeroAllowed, dynamicAllowed)
		}
	}

	var freeSupports int
	var freeWire string
	if err := db.QueryRow(`SELECT supports_reasoning, reasoning_wire_format FROM models WHERE public_id = 'free-step'`).Scan(&freeSupports, &freeWire); err != nil {
		t.Fatalf("read free-step: %v", err)
	}
	if freeSupports != 0 || freeWire != "none" {
		t.Fatalf("OpenCodeFree metadata changed = %d/%q", freeSupports, freeWire)
	}
	if err := migrateFS(db, with038); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
