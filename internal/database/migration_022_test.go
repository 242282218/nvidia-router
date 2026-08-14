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

func TestMigration022BackfillsDeepSeekTimeouts(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql", "015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql",
		"017_admin_audit_logs.sql", "018_access_key_token_budget.sql", "019_model_pricing.sql",
		"020_latency_routing_and_embedding_cache.sql", "021_provider_credentials.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/022_deepseek_timeout_backfill.sql")
	if err != nil {
		t.Fatalf("read migration 022: %v", err)
	}
	with022 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with022[name] = file
	}
	with022["migrations/022_deepseek_timeout_backfill.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-022: %v", err)
	}

	// Reproduce the production shape: the legacy full-ID row predates the alias and
	// resolves to the slow upstream, while the alias already carries migration 016's
	// override. A non-deepseek model and a deepseek model with an operator override
	// must be left untouched.
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, created_at, updated_at)
		VALUES ('deepseek-ai/deepseek-v4-flash', 'deepseek-ai/deepseek-v4-flash-0731', 'DeepSeek V4 Flash', 'chat', 1, '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert legacy deepseek row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, created_at, updated_at, stream_first_token_timeout_ms, stream_idle_timeout_ms)
		VALUES ('deepseek-custom', 'deepseek-ai/deepseek-v4-flash-0731', 'DeepSeek Custom', 'chat', 1, '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z', 120000, 120000)
	`); err != nil {
		t.Fatalf("insert custom deepseek row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, created_at, updated_at)
		VALUES ('minimaxai/minimax-m3', 'minimaxai/minimax-m3', 'MiniMax M3', 'chat', 1, '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert minimax row: %v", err)
	}

	if err := migrateFS(db, with022); err != nil {
		t.Fatalf("migrate 022: %v", err)
	}

	// Legacy full-ID alias backfilled to 300s.
	var firstToken, idle int
	if err := db.QueryRow(`SELECT stream_first_token_timeout_ms, stream_idle_timeout_ms FROM models WHERE public_id = 'deepseek-ai/deepseek-v4-flash'`).Scan(&firstToken, &idle); err != nil {
		t.Fatalf("read legacy deepseek timeouts: %v", err)
	}
	if firstToken != 300000 || idle != 300000 {
		t.Fatalf("legacy deepseek timeouts = %d/%d, want 300000/300000", firstToken, idle)
	}

	// Alias already seeded by 016 keeps its value (still 300s).
	if err := db.QueryRow(`SELECT stream_first_token_timeout_ms, stream_idle_timeout_ms FROM models WHERE public_id = 'deepseek-v4-flash'`).Scan(&firstToken, &idle); err != nil {
		t.Fatalf("read alias deepseek timeouts: %v", err)
	}
	if firstToken != 300000 || idle != 300000 {
		t.Fatalf("alias deepseek timeouts = %d/%d, want 300000/300000", firstToken, idle)
	}

	// Operator override is not clobbered.
	if err := db.QueryRow(`SELECT stream_first_token_timeout_ms, stream_idle_timeout_ms FROM models WHERE public_id = 'deepseek-custom'`).Scan(&firstToken, &idle); err != nil {
		t.Fatalf("read custom deepseek timeouts: %v", err)
	}
	if firstToken != 120000 || idle != 120000 {
		t.Fatalf("custom deepseek timeouts = %d/%d, want 120000/120000", firstToken, idle)
	}

	// Non-deepseek model stays on null (global default).
	var nullFirst, nullIdle any
	if err := db.QueryRow(`SELECT stream_first_token_timeout_ms, stream_idle_timeout_ms FROM models WHERE public_id = 'minimaxai/minimax-m3'`).Scan(&nullFirst, &nullIdle); err != nil {
		t.Fatalf("read minimax timeouts: %v", err)
	}
	if nullFirst != nil || nullIdle != nil {
		t.Fatalf("minimax timeouts = %v/%v, want nil/nil", nullFirst, nullIdle)
	}

	if err := migrateFS(db, with022); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
