package database

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration046AddsRequestLogCapabilitiesWithoutChangingExistingRows(t *testing.T) {
	prior := migrationFixture(t, migrationsBefore(t, 46))
	with046 := addMigration(t, prior, "046_request_log_capabilities.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-046: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO request_logs (request_id, endpoint, is_stream, http_status, outcome, duration_ms, attempt_count, created_at)
		VALUES ('legacy-request', '/v1/models', 0, 200, 'success', 1, 1, '2026-08-26T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed legacy request log: %v", err)
	}

	if err := migrateFS(db, with046); err != nil {
		t.Fatalf("migrate 046: %v", err)
	}
	var capabilities sql.NullString
	if err := db.QueryRow("SELECT requested_capabilities FROM request_logs WHERE request_id = 'legacy-request'").Scan(&capabilities); err != nil {
		t.Fatalf("read migrated request log: %v", err)
	}
	if capabilities.Valid {
		t.Fatalf("legacy requested_capabilities = %#v, want NULL", capabilities)
	}

	if err := migrateFS(db, with046); err != nil {
		t.Fatalf("repeat migration 046: %v", err)
	}
}
