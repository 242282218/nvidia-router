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

func TestMigration013AddsFirstTokenColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/013_first_token_ms.sql")
	if err != nil {
		t.Fatalf("read migration 013: %v", err)
	}
	with013 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with013[name] = file
	}
	with013["migrations/013_first_token_ms.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-013: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO request_logs (
			request_id, endpoint, http_status, outcome, is_stream, queue_ms,
			duration_ms, attempt_count, created_at
		) VALUES ('pre-013', '/v1/chat/completions', 200, 'success', 1, 0, 100, 1, '2026-08-08T00:00:00.000Z')
	`); err != nil {
		t.Fatalf("insert pre-013 request log: %v", err)
	}
	if err := migrateFS(db, with013); err != nil {
		t.Fatalf("migrate 013: %v", err)
	}
	for _, column := range []string{"total_first_token_ms", "first_token_count"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('daily_stats') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatalf("inspect daily_stats.%s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("daily_stats column %s count = %d", column, count)
		}
	}
	var firstToken *int64
	if err := db.QueryRow("SELECT first_token_ms FROM request_logs WHERE request_id = 'pre-013'").Scan(&firstToken); err != nil {
		t.Fatalf("read pre-013 first_token_ms: %v", err)
	}
	if firstToken != nil {
		t.Fatalf("pre-013 first_token_ms = %d, want NULL", *firstToken)
	}
	if err := migrateFS(db, with013); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
