package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
)

// When the app owns the database file it must open the separate read-only pool.
// Without this the reader silently stays nil and every read falls back to the
// single-connection writer, which is the behaviour this split exists to remove.
func TestNewOpensReaderPoolWhenOwningDatabaseFile(t *testing.T) {
	dataDir := t.TempDir()
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: dataDir, MasterKey: [32]byte{1}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	if app.dbReader == nil {
		t.Fatal("expected a read-only pool when the app owns the database file")
	}
	if app.dbReader == app.db {
		t.Fatal("reader pool must be a distinct handle from the writer pool")
	}

	var count int
	if err := app.dbReader.QueryRow("SELECT COUNT(*) FROM access_keys").Scan(&count); err != nil {
		t.Fatalf("read through reader pool: %v", err)
	}

	// The reader must be incapable of writing, not merely expected not to.
	_, err = app.dbReader.Exec("INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES ('x', x'00', 'nvr_x', '2026-01-01T00:00:00Z')")
	if err == nil {
		t.Fatal("expected the reader pool to reject writes")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Fatalf("reader rejection reason = %v", err)
	}

	if _, err := app.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("writer still usable: %v", err)
	}
	if filepath.Dir(filepath.Join(dataDir, routerDBFilename)) != dataDir {
		t.Fatal("unexpected database path layout")
	}

	response := httptestGet(t, app.Handler(), "/health/live")
	if response.Code != http.StatusOK {
		t.Fatalf("live status = %d", response.Code)
	}
}

// An injected DB has no path to reopen, so the reader stays nil and every
// repository falls back to the writer. Tests and the CLI rely on this.
func TestNewLeavesReaderNilForInjectedDatabase(t *testing.T) {
	db := openAppDatabase(t)
	defer func() { _ = db.Close() }()
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app.dbReader != nil {
		t.Fatal("expected no reader pool for an injected database")
	}

	// The app must stay fully functional with every read falling back to the
	// writer pool. Repository-level fallback is covered by the accesskey and
	// observability suites, which construct repositories without a reader.
	response := httptestGet(t, app.Handler(), "/health/live")
	if response.Code != http.StatusOK {
		t.Fatalf("live status = %d, want 200", response.Code)
	}
}
