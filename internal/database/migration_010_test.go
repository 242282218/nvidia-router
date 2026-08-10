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

func TestMigration010AddsMasterKeyVersionColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/010_master_key_versions.sql")
	if err != nil {
		t.Fatalf("read migration 010: %v", err)
	}
	with010 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with010[name] = file
	}
	with010["migrations/010_master_key_versions.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-010: %v", err)
	}
	if _, err := db.Exec("INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES ('test', X'01', 'nvr_test', '2026-08-05T00:00:00Z')"); err != nil {
		t.Fatalf("insert pre-010 access key: %v", err)
	}
	if _, err := db.Exec("INSERT INTO nvidia_keys (ciphertext, nonce, fingerprint, display_prefix, display_suffix, created_at, updated_at) VALUES (X'01', X'02', X'03', 'nv', 'key', '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z')"); err != nil {
		t.Fatalf("insert pre-010 NVIDIA key: %v", err)
	}
	if err := migrateFS(db, with010); err != nil {
		t.Fatalf("migrate 010: %v", err)
	}
	for _, table := range []string{"crypto_sentinel", "nvidia_keys", "proxy_pool_settings", "access_keys", "admin_sessions"} {
		column := "key_version"
		if table == "access_keys" || table == "admin_sessions" {
			column = "digest_key_version"
		}
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&count); err != nil {
			t.Fatalf("inspect %s.%s: %v", table, column, err)
		}
		if count != 1 {
			t.Fatalf("column %s.%s count = %d", table, column, count)
		}
	}
	var digestVersion int
	if err := db.QueryRow("SELECT digest_key_version FROM access_keys WHERE id = 1").Scan(&digestVersion); err != nil {
		t.Fatalf("read digest version: %v", err)
	}
	if digestVersion != 1 {
		t.Fatalf("digest version = %d, want 1", digestVersion)
	}
	for _, statement := range []string{
		"UPDATE access_keys SET digest_key_version = 0 WHERE id = 1",
		"UPDATE nvidia_keys SET key_version = 0 WHERE id = 1",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("invalid key version accepted: %s", statement)
		}
	}
	if err := migrateFS(db, with010); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
