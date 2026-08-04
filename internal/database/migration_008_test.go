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

// TestMigration008AddsRetryBudgetColumns locks in the retry-budget schema:
// runtime_settings gains max_attempts_per_request (default 5, CHECK 1..50) and
// retry_budget_ms (default 120000, CHECK 1000..600000). Before this migration a
// request retried until it had tried every key in the pool, and a streaming
// request had no total deadline at all, so the retry loop was unbounded.
func TestMigration008AddsRetryBudgetColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql",
		"002_indexes.sql",
		"003_xk_proxy_settings.sql",
		"004_observability_indexes.sql",
		"005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql",
		"007_monitoring_retention.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents008, err := fs.ReadFile(embeddedMigrations, "migrations/008_retry_budget.sql")
	if err != nil {
		t.Fatalf("read migration 008: %v", err)
	}
	with008 := make(fstest.MapFS, len(prior)+1)
	for k, v := range prior {
		with008[k] = v
	}
	with008["migrations/008_retry_budget.sql"] = &fstest.MapFile{Data: contents008}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Phase 1: 001-007 must not already carry the retry-budget columns.
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrateFS pre-008: %v", err)
	}
	names := tableColumns(t, db, "runtime_settings")
	if _, ok := names["max_attempts_per_request"]; ok {
		t.Fatal("pre-008 schema already had max_attempts_per_request")
	}
	if _, ok := names["retry_budget_ms"]; ok {
		t.Fatal("pre-008 schema already had retry_budget_ms")
	}

	// Phase 2: after 008 both columns exist with the documented defaults.
	if err := migrateFS(db, with008); err != nil {
		t.Fatalf("migrateFS migration 008: %v", err)
	}
	var maxAttempts, retryBudgetMS int
	if err := db.QueryRow("SELECT max_attempts_per_request, retry_budget_ms FROM runtime_settings WHERE id = 1").Scan(&maxAttempts, &retryBudgetMS); err != nil {
		t.Fatalf("read new columns: %v", err)
	}
	if maxAttempts != 5 {
		t.Fatalf("max_attempts_per_request default = %d, want 5", maxAttempts)
	}
	if retryBudgetMS != 120000 {
		t.Fatalf("retry_budget_ms default = %d, want 120000", retryBudgetMS)
	}

	// Phase 3: the CHECKs must reject values the router would misread. A 0
	// attempt cap would stop every request before its first key.
	for _, statement := range []string{
		"UPDATE runtime_settings SET max_attempts_per_request = 0 WHERE id = 1",
		"UPDATE runtime_settings SET max_attempts_per_request = 51 WHERE id = 1",
		"UPDATE runtime_settings SET retry_budget_ms = 999 WHERE id = 1",
		"UPDATE runtime_settings SET retry_budget_ms = 600001 WHERE id = 1",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("UPDATE accepted an out-of-range value (CHECK missing): %s", statement)
		}
	}

	// Phase 4: re-running must be a checksum-verify no-op, since ALTER TABLE ADD
	// COLUMN fails on a second execute.
	if err := migrateFS(db, with008); err != nil {
		t.Fatalf("migrateFS idempotent re-run: %v", err)
	}
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	names := map[string]struct{}{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dfltValue any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		names[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	return names
}
