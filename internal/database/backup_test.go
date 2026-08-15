package database

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
)

func TestBackupCopiesConsistentWALDatabase(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "router.db")
	db, err := Open(sourcePath)
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	})

	if _, err := db.Exec("PRAGMA wal_autocheckpoint = 0"); err != nil {
		t.Fatalf("disable WAL autocheckpoint: %v", err)
	}
	insertBackupFixtures(t, db)
	if info, err := os.Stat(sourcePath + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("source WAL is not populated: info=%v err=%v", info, err)
	}

	backupDir := filepath.Join(root, "backup dir")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	backupPath := filepath.Join(backupDir, "router #1.db")
	if err := Backup(context.Background(), db, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(backupPath)
		if err != nil {
			t.Fatalf("stat backup: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("backup permissions = %o, want 600", got)
		}
	}

	backupDB, err := Open(backupPath)
	if err != nil {
		t.Fatalf("Open backup: %v", err)
	}
	t.Cleanup(func() {
		if err := backupDB.Close(); err != nil {
			t.Errorf("close backup database: %v", err)
		}
	})
	assertBackupContents(t, backupDB)
}

func TestBackupRemovesTemporaryFileWhenSourceIsClosed(t *testing.T) {
	root := t.TempDir()
	db, err := Open(filepath.Join(root, "router.db"))
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	backupDir := filepath.Join(root, "backups")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	backupPath := filepath.Join(backupDir, "router.db")
	if err := Backup(context.Background(), db, backupPath); err == nil {
		t.Fatal("Backup succeeded with a closed source database")
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("backup failure left %d files", len(entries))
	}
}

func TestBackupOverwritesExistingDestination(t *testing.T) {
	// `db backup --output existing.db` must refresh a pre-existing file. On
	// platforms where os.Rename refuses to replace an existing target the
	// Backup fallback removes it first, so a second backup to the same path
	// must still publish the newest contents (audit #47).
	root := t.TempDir()
	sourcePath := filepath.Join(root, "router.db")
	db, err := Open(sourcePath)
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	})

	insertBackupFixtures(t, db)
	backupPath := filepath.Join(root, "router-backup.db")
	if err := Backup(context.Background(), db, backupPath); err != nil {
		t.Fatalf("first Backup: %v", err)
	}
	if _, err := db.Exec("UPDATE admins SET username = 'renamed' WHERE id = 1"); err != nil {
		t.Fatalf("mutate source after first backup: %v", err)
	}
	// Re-run with a target that already exists from the first backup.
	if err := Backup(context.Background(), db, backupPath); err != nil {
		t.Fatalf("second Backup over existing file: %v", err)
	}

	backupDB, err := Open(backupPath)
	if err != nil {
		t.Fatalf("Open overwritten backup: %v", err)
	}
	t.Cleanup(func() {
		if err := backupDB.Close(); err != nil {
			t.Errorf("close overwritten backup: %v", err)
		}
	})
	var username string
	if err := backupDB.QueryRow("SELECT username FROM admins WHERE id = 1").Scan(&username); err != nil {
		t.Fatalf("read overwritten backup admin: %v", err)
	}
	if username != "renamed" {
		t.Fatalf("overwritten backup username = %q, want latest source value", username)
	}
}

func TestBackupRejectsMigrationLedgerMismatch(t *testing.T) {
	root := t.TempDir()
	db, err := Open(filepath.Join(root, "router.db"))
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1"); err != nil {
		t.Fatalf("tamper migration ledger: %v", err)
	}

	backupPath := filepath.Join(root, "backup.db")
	if err := Backup(context.Background(), db, backupPath); err == nil {
		t.Fatal("Backup accepted a database with a mismatched migration checksum")
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("rejected backup destination stat error = %v, want not exist", err)
	}
}

func TestBackupRejectsIntegrityFailure(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "router.db")
	db, err := Open(sourcePath)
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	if _, err := db.Exec(`
		PRAGMA writable_schema = ON;
		UPDATE sqlite_master SET rootpage = 999999 WHERE type = 'table' AND name = 'admins';
		PRAGMA writable_schema = OFF;
	`); err != nil {
		t.Fatalf("corrupt SQLite root page: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close corrupted source: %v", err)
	}
	db = nil
	rawDSN := "file:" + url.PathEscape(sourcePath)
	db, err = driver.Open(rawDSN, nil)
	if err != nil {
		t.Fatalf("open corrupted source: %v", err)
	}

	backupPath := filepath.Join(root, "backup.db")
	if err := Backup(context.Background(), db, backupPath); err == nil {
		t.Fatal("Backup accepted a database that fails SQLite integrity checks")
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("rejected backup destination stat error = %v, want not exist", err)
	}
}

func insertBackupFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO admins (id, username, password_hash, must_change_password, created_at, updated_at)
		VALUES (1, 'admin', 'backup-hash', 0, '2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert admin fixture: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix,
			created_at, updated_at
		) VALUES (?, ?, ?, 'nvapi-', '-tail', '2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z')
	`, []byte("ciphertext-fixture"), []byte("nonce-fixture"), []byte("fingerprint-fixture")); err != nil {
		t.Fatalf("insert NVIDIA Key fixture: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO access_keys (name, key_digest, key_prefix, created_at)
		VALUES ('test client', ?, 'nvr_example', '2026-07-29T00:00:00Z')
	`, []byte("access-digest-fixture")); err != nil {
		t.Fatalf("insert Access Key fixture: %v", err)
	}
}

func assertBackupContents(t *testing.T, db *sql.DB) {
	t.Helper()
	var migrationCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("read backup schema: %v", err)
	}
	if migrationCount == 0 {
		t.Fatal("backup has no migration records")
	}
	var username string
	if err := db.QueryRow("SELECT username FROM admins WHERE id = 1").Scan(&username); err != nil || username != "admin" {
		t.Fatalf("backup admin = %q, err=%v", username, err)
	}
	var ciphertext, nonce, fingerprint []byte
	if err := db.QueryRow("SELECT ciphertext, nonce, fingerprint FROM nvidia_keys").Scan(&ciphertext, &nonce, &fingerprint); err != nil {
		t.Fatalf("read backup NVIDIA Key: %v", err)
	}
	if !bytes.Equal(ciphertext, []byte("ciphertext-fixture")) ||
		!bytes.Equal(nonce, []byte("nonce-fixture")) ||
		!bytes.Equal(fingerprint, []byte("fingerprint-fixture")) {
		t.Fatal("backup changed NVIDIA Key encrypted fields")
	}
	var accessName, accessPrefix string
	if err := db.QueryRow("SELECT name, key_prefix FROM access_keys").Scan(&accessName, &accessPrefix); err != nil {
		t.Fatalf("read backup Access Key: %v", err)
	}
	if accessName != "test client" || accessPrefix != "nvr_example" {
		t.Fatalf("backup Access Key = %q/%q", accessName, accessPrefix)
	}
}
