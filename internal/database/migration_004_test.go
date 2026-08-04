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

// TestMigration004NormalizesLegacyTimestamps locks in the behavior that
// migration 004 rewrites legacy RFC3339Nano created_at values into the
// fixed-width millisecond format the observability layer relies on for
// lexicographic comparisons.
func TestMigration004NormalizesLegacyTimestamps(t *testing.T) {
	initial, err := fs.ReadFile(embeddedMigrations, "migrations/001_initial.sql")
	if err != nil {
		t.Fatalf("read initial migration: %v", err)
	}
	indexes, err := fs.ReadFile(embeddedMigrations, "migrations/002_indexes.sql")
	if err != nil {
		t.Fatalf("read migration 002: %v", err)
	}
	xkProxy, err := fs.ReadFile(embeddedMigrations, "migrations/003_xk_proxy_settings.sql")
	if err != nil {
		t.Fatalf("read migration 003: %v", err)
	}
	normalize, err := fs.ReadFile(embeddedMigrations, "migrations/004_observability_indexes.sql")
	if err != nil {
		t.Fatalf("read migration 004: %v", err)
	}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Simulate a pre-004 database: schema plus legacy RFC3339Nano rows.
	if err := migrateFS(db, fstest.MapFS{
		"migrations/001_initial.sql":           &fstest.MapFile{Data: initial},
		"migrations/002_indexes.sql":           &fstest.MapFile{Data: indexes},
		"migrations/003_xk_proxy_settings.sql": &fstest.MapFile{Data: xkProxy},
	}); err != nil {
		t.Fatalf("migrateFS pre-004: %v", err)
	}
	for _, fixture := range []struct {
		id string
		at string
	}{
		{"whole-second", "2026-06-30T03:00:00Z"},
		{"three-digits", "2026-06-30T03:00:00.949Z"},
		{"nine-digits", "2026-06-30T03:00:00.900123456Z"},
	} {
		if _, err := db.Exec(`
			INSERT INTO request_logs (
				request_id, endpoint, http_status, outcome, is_stream, duration_ms, attempt_count, created_at
			) VALUES (?, 'migration-test', 200, 'success', 0, 1, 1, ?)
		`, fixture.id, fixture.at); err != nil {
			t.Fatalf("insert legacy row %s: %v", fixture.id, err)
		}
	}

	if err := migrateFS(db, fstest.MapFS{
		"migrations/001_initial.sql":           &fstest.MapFile{Data: initial},
		"migrations/002_indexes.sql":           &fstest.MapFile{Data: indexes},
		"migrations/003_xk_proxy_settings.sql": &fstest.MapFile{Data: xkProxy},
		"migrations/004_observability_indexes.sql": &fstest.MapFile{Data: normalize},
	}); err != nil {
		t.Fatalf("migrateFS migration 004: %v", err)
	}

	for _, fixture := range []struct {
		id   string
		want string
	}{
		{"whole-second", "2026-06-30T03:00:00.000Z"},
		{"three-digits", "2026-06-30T03:00:00.949Z"},
		{"nine-digits", "2026-06-30T03:00:00.900Z"},
	} {
		var normalized string
		if err := db.QueryRow("SELECT created_at FROM request_logs WHERE request_id = ?", fixture.id).Scan(&normalized); err != nil {
			t.Fatalf("read normalized %s: %v", fixture.id, err)
		}
		if normalized != fixture.want {
			t.Fatalf("normalized %s = %q, want %q", fixture.id, normalized, fixture.want)
		}
	}
	if !hasIndex(t, db, "idx_request_logs_outcome_error_created") {
		t.Fatal("migration 004 did not create idx_request_logs_outcome_error_created")
	}
}
