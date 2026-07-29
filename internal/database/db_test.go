package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenConfiguresSQLiteAndMigratesSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	assertPragmaText(t, db, "journal_mode", "wal")
	assertPragmaInt(t, db, "foreign_keys", 1)
	assertPragmaInt(t, db, "busy_timeout", 5000)
	assertPragmaInt(t, db, "synchronous", 1)
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}

	var settings struct {
		queueCapacity           int
		queueWaitTimeoutMS      int
		connectTimeoutMS        int
		firstByteTimeoutMS      int
		nonstreamTotalTimeoutMS int
		shutdownGraceMS         int
		updatedAt               string
	}
	err = db.QueryRow(`
		SELECT queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
		       first_byte_timeout_ms, nonstream_total_timeout_ms,
		       shutdown_grace_ms, updated_at
		FROM runtime_settings
		WHERE id = 1
	`).Scan(
		&settings.queueCapacity,
		&settings.queueWaitTimeoutMS,
		&settings.connectTimeoutMS,
		&settings.firstByteTimeoutMS,
		&settings.nonstreamTotalTimeoutMS,
		&settings.shutdownGraceMS,
		&settings.updatedAt,
	)
	if err != nil {
		t.Fatalf("query default runtime settings: %v", err)
	}

	wantSettings := struct {
		queueCapacity           int
		queueWaitTimeoutMS      int
		connectTimeoutMS        int
		firstByteTimeoutMS      int
		nonstreamTotalTimeoutMS int
		shutdownGraceMS         int
		updatedAt               string
	}{100, 60000, 10000, 60000, 300000, 60000, "1970-01-01T00:00:00Z"}
	if settings != wantSettings {
		t.Fatalf("runtime settings = %+v, want %+v", settings, wantSettings)
	}

	var adminCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM admins").Scan(&adminCount); err != nil {
		t.Fatalf("query admins: %v", err)
	}
	if adminCount != 0 {
		t.Fatalf("admin count = %d, want 0", adminCount)
	}
}

func TestOpenConfiguresReplacementConnections(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	db.SetMaxIdleConns(0)
	assertPragmaText(t, db, "journal_mode", "wal")
	assertPragmaInt(t, db, "foreign_keys", 1)
	assertPragmaInt(t, db, "busy_timeout", 5000)
	assertPragmaInt(t, db, "synchronous", 1)

	_, err = db.Exec(`
		INSERT INTO nvidia_key_model_blocks (
			nvidia_key_id, model_id, reason_code, first_seen_at, last_seen_at
		) VALUES (999, 999, 'test', '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z')
	`)
	if err == nil {
		t.Fatal("invalid foreign-key insert succeeded after replacing the connection")
	}
}

func assertPragmaText(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()

	var got string
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("query PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}

func assertPragmaInt(t *testing.T, db *sql.DB, name string, want int) {
	t.Helper()

	var got int
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("query PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
	}
}
