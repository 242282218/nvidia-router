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

func TestMigration017CreatesAdminAuditLogs(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql", "015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/017_admin_audit_logs.sql")
	if err != nil {
		t.Fatalf("read migration 017: %v", err)
	}
	with017 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with017[name] = file
	}
	with017["migrations/017_admin_audit_logs.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-017: %v", err)
	}
	if err := migrateFS(db, with017); err != nil {
		t.Fatalf("migrate 017: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO admin_audit_logs (action, target_type, target_id, detail, session_id, client_ip, created_at)
		VALUES ('nvidia_key.import', 'nvidia_key', '3', '{"imported":true}', 'sess-1', '127.0.0.1', '2026-08-11T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert audit row: %v", err)
	}

	var action string
	var count int
	if err := db.QueryRow(`SELECT action, COUNT(*) FROM admin_audit_logs GROUP BY action`).Scan(&action, &count); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != "nvidia_key.import" || count != 1 {
		t.Fatalf("audit row = action:%s count:%d, want nvidia_key.import 1", action, count)
	}

	if err := migrateFS(db, with017); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
