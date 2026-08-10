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

func TestMigration014AddsStreamTimeoutColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/014_stream_timeouts.sql")
	if err != nil {
		t.Fatalf("read migration 014: %v", err)
	}
	with014 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with014[name] = file
	}
	with014["migrations/014_stream_timeouts.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-014: %v", err)
	}
	if err := migrateFS(db, with014); err != nil {
		t.Fatalf("migrate 014: %v", err)
	}

	var firstTokenDefault, idleDefault int
	if err := db.QueryRow("SELECT stream_first_token_timeout_ms, stream_idle_timeout_ms FROM runtime_settings WHERE id = 1").Scan(&firstTokenDefault, &idleDefault); err != nil {
		t.Fatalf("read stream timeout defaults: %v", err)
	}
	if firstTokenDefault != 60000 {
		t.Fatalf("default stream_first_token_timeout_ms = %d, want 60000", firstTokenDefault)
	}
	if idleDefault != 180000 {
		t.Fatalf("default stream_idle_timeout_ms = %d, want 180000", idleDefault)
	}
	for _, invalid := range []int{0, 999, 1800001} {
		if _, err := db.Exec("UPDATE runtime_settings SET stream_first_token_timeout_ms = ? WHERE id = 1", invalid); err == nil {
			t.Fatalf("invalid stream_first_token_timeout_ms %d accepted", invalid)
		}
		if _, err := db.Exec("UPDATE runtime_settings SET stream_idle_timeout_ms = ? WHERE id = 1", invalid); err == nil {
			t.Fatalf("invalid stream_idle_timeout_ms %d accepted", invalid)
		}
	}
	if err := migrateFS(db, with014); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
