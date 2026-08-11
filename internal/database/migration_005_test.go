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

// TestMigration005AddsFailoverAndRetentionColumns locks in the schema extension
// for the gpt-load comparison plan audits B4/B5: runtime_settings gains
// failover_status_codes (TEXT, default "429,500,502,503,504") and
// request_log_retention_days (INTEGER, default 30, CHECK 1..365). The migration
// is idempotent under the migrateFS ledger (subsequent runs only verify the
// checksum) and rejects out-of-range storage applied outside the CHECK.
func TestMigration005AddsFailoverAndRetentionColumns(t *testing.T) {
	// Reuse the prior migrations verbatim so the simulated pre-migration schema
	// matches what a 004-final database looks like before the operator upgrades.
	priorFiles := []string{
		"001_initial.sql",
		"002_indexes.sql",
		"003_xk_proxy_settings.sql",
		"004_observability_indexes.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents005, err := fs.ReadFile(embeddedMigrations, "migrations/005_runtime_failover_and_retention.sql")
	if err != nil {
		t.Fatalf("read migration 005: %v", err)
	}
	with005 := make(fstest.MapFS, len(prior)+1)
	for k, v := range prior {
		with005[k] = v
	}
	with005["migrations/005_runtime_failover_and_retention.sql"] = &fstest.MapFile{Data: contents005}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Phase 1: apply migrations 001-004 only and confirm runtime_settings still
	// lacks the new columns; the legacy default values must come from the
	// application layer, not the column default (the column does not exist).
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrateFS pre-005: %v", err)
	}
	rows, err := db.Query("PRAGMA table_info(runtime_settings)")
	if err != nil {
		t.Fatalf("table_info pre-005: %v", err)
	}
	names := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			_ = rows.Close()
			t.Fatalf("scan table_info pre-005: %v", err)
		}
		names[name] = struct{}{}
	}
	_ = rows.Close()
	if _, ok := names["failover_status_codes"]; ok {
		t.Fatal("pre-005 schema already had failover_status_codes")
	}
	if _, ok := names["request_log_retention_days"]; ok {
		t.Fatal("pre-005 schema already had request_log_retention_days")
	}

	// Phase 2: apply migration 005 and confirm the columns appear with the
	// documented defaults and that the defaults round-trip through a fresh
	// SELECT.
	if err := migrateFS(db, with005); err != nil {
		t.Fatalf("migrateFS migration 005: %v", err)
	}
	var failoverCodes string
	var retentionDays int
	if err := db.QueryRow("SELECT failover_status_codes, request_log_retention_days FROM runtime_settings WHERE id = 1").Scan(&failoverCodes, &retentionDays); err != nil {
		t.Fatalf("read new columns: %v", err)
	}
	if failoverCodes != "429,500,502,503,504" {
		t.Fatalf("failover_status_codes default = %q, want %q", failoverCodes, "429,500,502,503,504")
	}
	if retentionDays != 30 {
		t.Fatalf("request_log_retention_days default = %d, want 30", retentionDays)
	}

	// Phase 3: the CHECK on request_log_retention_days must reject 0 and 366
	// so the router cannot persist a value the cleanup worker would misread.
	if _, err := db.Exec("UPDATE runtime_settings SET request_log_retention_days = 0 WHERE id = 1"); err == nil {
		t.Fatal("UPDATE accepted request_log_retention_days = 0 (CHECK missing)")
	}
	if _, err := db.Exec("UPDATE runtime_settings SET request_log_retention_days = 366 WHERE id = 1"); err == nil {
		t.Fatal("UPDATE accepted request_log_retention_days = 366 (CHECK missing)")
	}

	// Phase 4: re-running migrations must be a no-op checksum-verify pass, not a
	// re-execute: ALTER TABLE ADD COLUMN would fail twice. This protects the
	// common upgrade path where the operator restarted without a clean database.
	if err := migrateFS(db, with005); err != nil {
		t.Fatalf("migrateFS idempotent re-run: %v", err)
	}
}
