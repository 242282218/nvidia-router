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

func TestMigration018AddsAccessKeyTokenBudgetColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql", "015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql",
		"017_admin_audit_logs.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/018_access_key_token_budget.sql")
	if err != nil {
		t.Fatalf("read migration 018: %v", err)
	}
	with018 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with018[name] = file
	}
	with018["migrations/018_access_key_token_budget.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-018: %v", err)
	}

	// A pre-existing key must default to unlimited capacity after migration.
	if _, err := db.Exec(`
		INSERT INTO access_keys (name, key_digest, key_prefix, created_at)
		VALUES ('legacy', x'0102', 'nvr_legacy', '2026-07-30T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert legacy key: %v", err)
	}
	if err := migrateFS(db, with018); err != nil {
		t.Fatalf("migrate 018: %v", err)
	}

	var budget, consumed int64
	if err := db.QueryRow(`SELECT token_budget, consumed_tokens FROM access_keys WHERE key_prefix = 'nvr_legacy'`).Scan(&budget, &consumed); err != nil {
		t.Fatalf("read legacy key budget: %v", err)
	}
	if budget != 0 || consumed != 0 {
		t.Fatalf("legacy key default budget/consumed = %d/%d, want 0/0", budget, consumed)
	}

	if _, err := db.Exec(`UPDATE access_keys SET token_budget = 1000000, consumed_tokens = 250000 WHERE key_prefix = 'nvr_legacy'`); err != nil {
		t.Fatalf("update budget: %v", err)
	}
	var updated int64
	if err := db.QueryRow(`SELECT consumed_tokens FROM access_keys WHERE key_prefix = 'nvr_legacy'`).Scan(&updated); err != nil {
		t.Fatalf("read updated consumed: %v", err)
	}
	if updated != 250000 {
		t.Fatalf("updated consumed = %d, want 250000", updated)
	}

	if err := migrateFS(db, with018); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
