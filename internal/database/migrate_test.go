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
	if _, err := db.Exec("DELETE FROM schema_migrations"); err != nil {
		t.Fatalf("clear migration ledger fixture: %v", err)
	}

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
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version > 1"); err != nil {
		t.Fatalf("trim migration ledger fixture: %v", err)
	}

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

func TestMigrateRejectsUnknownVersionBeforeApplyingMigrations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	for _, indexName := range []string{
		"idx_admin_sessions_expires",
		"idx_nvidia_keys_schedulable",
		"idx_models_enabled_kind",
		"idx_key_model_blocks_model",
		"idx_access_keys_active",
		"idx_request_logs_created",
		"idx_request_logs_model_created",
		"idx_request_logs_nvidia_key_created",
		"idx_request_logs_access_key_created",
	} {
		if _, err := db.Exec("DROP INDEX " + indexName); err != nil {
			t.Fatalf("remove migration-created index %q: %v", indexName, err)
		}
	}
	before := readTestMigrationLedger(t, db)
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = 2; INSERT INTO schema_migrations (version, checksum, applied_at) VALUES (9001, 'future-checksum', 'future-time')"); err != nil {
		t.Fatalf("insert unknown migration: %v", err)
	}
	afterMutation := readTestMigrationLedger(t, db)

	err = Migrate(db)
	if err == nil {
		t.Fatal("Migrate accepted unknown migration version")
	}
	for _, part := range []string{"unknown migration version", "9001"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("Migrate error = %q, want substring %q", err, part)
		}
	}
	if hasIndex(t, db, "idx_models_enabled_kind") {
		t.Fatal("Migrate applied a known migration after finding an unknown version")
	}
	if got := readTestMigrationLedger(t, db); !sameMigrationLedger(got, afterMutation) {
		t.Fatalf("migration ledger changed after rejected Migrate: before=%v after=%v", afterMutation, got)
	}
	if sameMigrationLedger(before, afterMutation) {
		t.Fatal("test fixture did not create a changed migration ledger")
	}
}

func TestVerifyMigrationsRejectsUnknownVersionWithoutChangingLedger(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("INSERT INTO schema_migrations (version, checksum, applied_at) VALUES (9001, 'future-checksum', 'future-time')"); err != nil {
		t.Fatalf("insert unknown migration: %v", err)
	}
	before := readTestMigrationLedger(t, db)

	err = VerifyMigrations(context.Background(), db)
	if err == nil {
		t.Fatal("VerifyMigrations accepted unknown migration version")
	}
	for _, part := range []string{"unknown migration version", "9001"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("VerifyMigrations error = %q, want substring %q", err, part)
		}
	}
	if got := readTestMigrationLedger(t, db); !sameMigrationLedger(got, before) {
		t.Fatalf("migration ledger changed after rejected VerifyMigrations: before=%v after=%v", before, got)
	}
}

type migrationLedgerRow struct {
	version   int
	checksum  string
	appliedAt string
}

func readTestMigrationLedger(t *testing.T, db *sql.DB) []migrationLedgerRow {
	t.Helper()
	rows, err := db.Query("SELECT version, checksum, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	defer rows.Close()

	var ledger []migrationLedgerRow
	for rows.Next() {
		var row migrationLedgerRow
		if err := rows.Scan(&row.version, &row.checksum, &row.appliedAt); err != nil {
			t.Fatalf("scan migration ledger: %v", err)
		}
		ledger = append(ledger, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration ledger: %v", err)
	}
	return ledger
}

func sameMigrationLedger(left, right []migrationLedgerRow) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func hasIndex(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).Scan(&count); err != nil {
		t.Fatalf("query index %q: %v", name, err)
	}
	return count == 1
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
