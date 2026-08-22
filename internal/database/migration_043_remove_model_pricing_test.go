package database

import (
	"database/sql"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigrationRemovesModelPricingColumns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	assertNoModelPricingColumns(t, db)
}

func TestMigration043PreservesExistingModelsAndUsage(t *testing.T) {
	prior := migrationFixture(t, migrationsBefore(t, 43))
	with043 := addMigration(t, prior, "043_remove_model_pricing.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-043: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled,
		                   input_usd_per_mtok, output_usd_per_mtok, created_at, updated_at)
		VALUES ('legacy-model', 'vendor/legacy', 'Legacy', 'chat', 1,
		        0.14, 0.28, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed legacy model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO daily_stats (day, dimension_type, dimension_id, request_count, success_count, failure_count,
		                        prompt_tokens, completion_tokens)
		VALUES ('2026-08-10', 'model', 'legacy-model', 2, 2, 0, 100, 25)
	`); err != nil {
		t.Fatalf("seed usage stats: %v", err)
	}

	if err := migrateFS(db, with043); err != nil {
		t.Fatalf("migrate 043: %v", err)
	}
	var modelID int64
	if err := db.QueryRow("SELECT id FROM models WHERE public_id = ?", "legacy-model").Scan(&modelID); err != nil {
		t.Fatalf("read preserved model: %v", err)
	}
	if modelID == 0 {
		t.Fatal("preserved model has no id")
	}
	var promptTokens, completionTokens int64
	if err := db.QueryRow(`SELECT prompt_tokens, completion_tokens FROM daily_stats WHERE dimension_id = ?`, "legacy-model").Scan(&promptTokens, &completionTokens); err != nil {
		t.Fatalf("read preserved usage: %v", err)
	}
	if promptTokens != 100 || completionTokens != 25 {
		t.Fatalf("preserved usage = %d/%d, want 100/25", promptTokens, completionTokens)
	}
	assertNoModelPricingColumns(t, db)

	if err := migrateFS(db, with043); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}

func migrationsBefore(t *testing.T, version int) []string {
	t.Helper()
	entries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		migrationVersion, err := migrationVersion(entry.Name())
		if err != nil {
			t.Fatalf("parse migration %s: %v", entry.Name(), err)
		}
		if migrationVersion < version {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func assertNoModelPricingColumns(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}) {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(models)")
	if err != nil {
		t.Fatalf("inspect models schema: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan models schema: %v", err)
		}
		if name == "input_usd_per_mtok" || name == "output_usd_per_mtok" {
			t.Fatalf("models still contains pricing column %q", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate models schema: %v", err)
	}
}
