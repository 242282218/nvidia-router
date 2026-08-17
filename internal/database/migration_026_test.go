package database

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration026AddsReasoningObservabilityColumns(t *testing.T) {
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
	})
	with026 := addMigration(t, prior, "026_reasoning_observability.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-026: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO request_logs (
		request_id, endpoint, model_id, http_status, outcome, is_stream,
		queue_ms, duration_ms, attempt_count, created_at
	) VALUES ('legacy-1', '/v1/chat/completions', 'z-ai/glm-5.2', 200, 'success', 0, 0, 100, 1, '2026-08-16T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy request log: %v", err)
	}
	if err := migrateFS(db, with026); err != nil {
		t.Fatalf("migrate 026: %v", err)
	}

	var hasColumn int
	for _, column := range []string{
		"reasoning_requested", "reasoning_wire_fields", "reasoning_present",
		"reasoning_chars", "stream_done", "route_mode",
	} {
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('request_logs') WHERE name = ?", column).Scan(&hasColumn); err != nil {
			t.Fatalf("query pragma for %s: %v", column, err)
		}
		if hasColumn != 1 {
			t.Fatalf("column %s missing after migration 026", column)
		}
	}

	var reasoningRequested, reasoningPresent, streamDone int
	var reasoningWireFields, reasoningChars, routeMode any
	if err := db.QueryRow(`SELECT reasoning_requested, reasoning_wire_fields, reasoning_present,
	       reasoning_chars, stream_done, route_mode FROM request_logs WHERE request_id = 'legacy-1'`).
		Scan(&reasoningRequested, &reasoningWireFields, &reasoningPresent, &reasoningChars, &streamDone, &routeMode); err != nil {
		t.Fatalf("read legacy row defaults: %v", err)
	}
	if reasoningRequested != 0 || reasoningPresent != 0 || streamDone != 0 {
		t.Fatalf("legacy row booleans = requested %d present %d done %d, want 0/0/0", reasoningRequested, reasoningPresent, streamDone)
	}
	if reasoningWireFields != nil || reasoningChars != nil || routeMode != nil {
		t.Fatalf("legacy row optional fields should be NULL, got %v / %v / %v", reasoningWireFields, reasoningChars, routeMode)
	}

	// Re-running the same migration set must be a no-op (idempotency).
	if err := migrateFS(db, with026); err != nil {
		t.Fatalf("re-migrate 026: %v", err)
	}
}
