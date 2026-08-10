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

func TestMigration011AddsMaxStreamingPerKeyColumn(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/011_streaming_quota.sql")
	if err != nil {
		t.Fatalf("read migration 011: %v", err)
	}
	with011 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with011[name] = file
	}
	with011["migrations/011_streaming_quota.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-011: %v", err)
	}
	if err := migrateFS(db, with011); err != nil {
		t.Fatalf("migrate 011: %v", err)
	}

	var defaultValue int
	if err := db.QueryRow("SELECT max_streaming_per_key FROM runtime_settings WHERE id = 1").Scan(&defaultValue); err != nil {
		t.Fatalf("read max_streaming_per_key default: %v", err)
	}
	if defaultValue != 2 {
		t.Fatalf("default max_streaming_per_key = %d, want 2", defaultValue)
	}
	for _, invalid := range []int{0, 11} {
		if _, err := db.Exec("UPDATE runtime_settings SET max_streaming_per_key = ? WHERE id = 1", invalid); err == nil {
			t.Fatalf("invalid max_streaming_per_key %d accepted", invalid)
		}
	}
	if err := migrateFS(db, with011); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
