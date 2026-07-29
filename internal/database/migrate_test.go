package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	err = migrateFS(db, fstest.MapFS{
		"migrations/003_broken.sql": &fstest.MapFile{Data: []byte(`
			CREATE TABLE rolled_back_table (id INTEGER PRIMARY KEY);
			THIS IS NOT SQL;
		`)},
		"migrations/004_later.sql": &fstest.MapFile{Data: []byte("CREATE TABLE must_not_exist (id INTEGER PRIMARY KEY);")},
	})
	if err == nil {
		t.Fatal("migrateFS succeeded with invalid migration SQL")
	}
	for _, part := range []string{"version 3", "003_broken.sql", "execute"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("migrateFS error = %q, want substring %q", err, part)
		}
	}

	assertTableDoesNotExist(t, db, "rolled_back_table")
	assertTableDoesNotExist(t, db, "must_not_exist")

	var migrationCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version IN (3, 4)").Scan(&migrationCount); err != nil {
		t.Fatalf("query failed migration ledger rows: %v", err)
	}
	if migrationCount != 0 {
		t.Fatalf("failed migration ledger rows = %d, want 0", migrationCount)
	}
}

func TestMigrateRejectsChangedChecksum(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	err = migrateFS(db, fstest.MapFS{
		"migrations/001_initial.sql": &fstest.MapFile{Data: []byte("CREATE TABLE checksum_tampered (id INTEGER PRIMARY KEY);")},
		"migrations/003_later.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE must_not_exist (id INTEGER PRIMARY KEY);")},
	})
	if err == nil {
		t.Fatal("migrateFS succeeded after migration checksum changed")
	}
	for _, part := range []string{"migration", "version 1", "checksum"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("migrateFS error = %q, want substring %q", err, part)
		}
	}

	assertTableDoesNotExist(t, db, "must_not_exist")
}

func TestVerifyMigrationsRejectsMissingOrChangedRecordedMigration(t *testing.T) {
	for _, mutate := range []struct {
		name string
		sql  string
	}{
		{"missing", "DELETE FROM schema_migrations WHERE version = 2"},
		{"checksum", "UPDATE schema_migrations SET checksum = 'invalid' WHERE version = 1"},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "router.db"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()
			if _, err := db.Exec(mutate.sql); err != nil {
				t.Fatalf("mutate migration ledger: %v", err)
			}
			if err := VerifyMigrations(context.Background(), db); err == nil {
				t.Fatal("VerifyMigrations accepted an incomplete migration ledger")
			}
		})
	}
}

func assertTableDoesNotExist(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		name,
	).Scan(&count); err != nil {
		t.Fatalf("query table %q: %v", name, err)
	}
	if count != 0 {
		t.Fatalf("table %q exists, want rolled back", name)
	}
}
