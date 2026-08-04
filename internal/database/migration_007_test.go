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

func TestMigration007NormalizesRetentionAndAddsMonitoringColumns(t *testing.T) {
	db := openPre007Database(t)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec("UPDATE runtime_settings SET request_log_retention_days = 7 WHERE id = 1"); err != nil {
		t.Fatalf("seed retention: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var retention int
	if err := db.QueryRow("SELECT request_log_retention_days FROM runtime_settings WHERE id = 1").Scan(&retention); err != nil {
		t.Fatalf("read retention: %v", err)
	}
	if retention != 30 {
		t.Fatalf("retention = %d, want 30", retention)
	}

	for _, column := range []string{"total_first_byte_ms", "first_byte_count"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('daily_stats') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatalf("read column %s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("column %s count = %d, want 1", column, count)
		}
	}

	if _, err := db.Exec("UPDATE runtime_settings SET request_log_retention_days = 29 WHERE id = 1"); err == nil {
		t.Fatal("UPDATE accepted request_log_retention_days = 29")
	}
	if _, err := db.Exec("UPDATE runtime_settings SET request_log_retention_days = 366 WHERE id = 1"); err == nil {
		t.Fatal("UPDATE accepted request_log_retention_days = 366")
	}
}

func openPre007Database(t *testing.T) *sql.DB {
	t.Helper()
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql",
	}
	migrations := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		migrations["migrations/"+name] = &fstest.MapFile{Data: contents}
	}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	if err := migrateFS(db, migrations); err != nil {
		_ = db.Close()
		t.Fatalf("migrate pre-007 database: %v", err)
	}
	return db
}
