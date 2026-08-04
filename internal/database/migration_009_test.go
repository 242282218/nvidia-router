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

func TestMigration009AddsAccessKeyPolicyColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/009_access_key_limits.sql")
	if err != nil {
		t.Fatalf("read migration 009: %v", err)
	}
	with009 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with009[name] = file
	}
	with009["migrations/009_access_key_limits.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-009: %v", err)
	}
	if _, err := db.Exec("INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES ('test', X'01', 'nvr_test', '2026-08-05T00:00:00Z')"); err != nil {
		t.Fatalf("insert pre-009 key: %v", err)
	}
	if err := migrateFS(db, with009); err != nil {
		t.Fatalf("migrate 009: %v", err)
	}

	var expires any
	var rpm, tpm, concurrent int
	if err := db.QueryRow("SELECT expires_at, rpm_limit, tpm_limit, max_concurrent FROM access_keys WHERE id = 1").Scan(&expires, &rpm, &tpm, &concurrent); err != nil {
		t.Fatalf("read policy columns: %v", err)
	}
	if expires != nil || rpm != 0 || tpm != 0 || concurrent != 0 {
		t.Fatalf("policy defaults = expires=%v rpm=%d tpm=%d concurrent=%d", expires, rpm, tpm, concurrent)
	}
	for _, statement := range []string{
		"UPDATE access_keys SET rpm_limit = -1 WHERE id = 1",
		"UPDATE access_keys SET rpm_limit = 100001 WHERE id = 1",
		"UPDATE access_keys SET tpm_limit = 1000000001 WHERE id = 1",
		"UPDATE access_keys SET max_concurrent = 10001 WHERE id = 1",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("out-of-range policy accepted: %s", statement)
		}
	}
	if err := migrateFS(db, with009); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
