package database

import (
	"database/sql"
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration012DropsXkProxySettings(t *testing.T) {
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
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/012_drop_xk_proxy_settings.sql")
	if err != nil {
		t.Fatalf("read migration 012: %v", err)
	}
	with012 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with012[name] = file
	}
	with012["migrations/012_drop_xk_proxy_settings.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-012: %v", err)
	}
	if count := countTables(db, "xk_proxy_settings"); count != 1 {
		t.Fatalf("pre-012 xk_proxy_settings table count = %d, want 1", count)
	}
	if err := migrateFS(db, with012); err != nil {
		t.Fatalf("migrate 012: %v", err)
	}
	if count := countTables(db, "xk_proxy_settings"); count != 0 {
		t.Fatalf("post-012 xk_proxy_settings table count = %d, want 0", count)
	}
	if err := migrateFS(db, with012); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}

func countTables(db *sql.DB, name string) int {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&count); err != nil {
		panic(err)
	}
	return count
}
