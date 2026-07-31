package health

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

func TestLiveReturnsExactOKResponse(t *testing.T) {
	handler := New(nil, nil, func() bool { return false })
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Body.String(); got != "{\"status\":\"ok\"}" {
		t.Fatalf("body = %q", got)
	}
}

func TestReadyReturnsServiceUnavailableWithoutOperationalDetails(t *testing.T) {
	db := readyDatabase(t)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("UPDATE admins SET must_change_password = 1 WHERE id = 1"); err != nil {
		t.Fatalf("require password change: %v", err)
	}
	handler := New(db, testKeys(t), func() bool { return false })
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Body.String(); got != "{\"status\":\"unavailable\"}" {
		t.Fatalf("body = %q", got)
	}
}

func TestReadyReturnsServiceUnavailableWhenDatabaseCannotBeReached(t *testing.T) {
	db := readyDatabase(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	assertReadyUnavailable(t, New(db, testKeys(t), func() bool { return false }))
}

func TestReadyReturnsServiceUnavailableWhenMigrationsAreUnavailable(t *testing.T) {
	db := readyDatabase(t)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("DROP TABLE schema_migrations"); err != nil {
		t.Fatalf("drop migration ledger: %v", err)
	}
	assertReadyUnavailable(t, New(db, testKeys(t), func() bool { return false }))
}

func TestReadyReturnsServiceUnavailableWhenSentinelIsUnavailable(t *testing.T) {
	db := readyDatabase(t)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("DELETE FROM crypto_sentinel WHERE id = 1"); err != nil {
		t.Fatalf("delete sentinel: %v", err)
	}
	assertReadyUnavailable(t, New(db, testKeys(t), func() bool { return false }))
}

func TestReadyReturnsServiceUnavailableForInvalidMigrationOrSentinel(t *testing.T) {
	for _, item := range []struct {
		name   string
		mutate func(*sql.DB) error
		keys   *crypto.KeySet
	}{
		{"checksum", func(db *sql.DB) error {
			_, err := db.Exec("UPDATE schema_migrations SET checksum = 'bad' WHERE version = 1")
			return err
		}, testKeys(t)},
		{"corrupt sentinel", func(db *sql.DB) error {
			_, err := db.Exec("UPDATE crypto_sentinel SET ciphertext = X'00' WHERE id = 1")
			return err
		}, testKeys(t)},
		{"wrong key", func(_ *sql.DB) error { return nil }, func() *crypto.KeySet { keys, _ := crypto.New([32]byte{2}); return keys }()},
	} {
		t.Run(item.name, func(t *testing.T) {
			db := readyDatabase(t)
			defer func() { _ = db.Close() }()
			if err := item.mutate(db); err != nil {
				t.Fatalf("mutate ready prerequisite: %v", err)
			}
			assertReadyUnavailable(t, New(db, item.keys, func() bool { return false }))
		})
	}
}

func TestReadyRejectsShutdown(t *testing.T) {
	handler := New(nil, nil, func() bool { return true })
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func assertReadyUnavailable(t *testing.T, handler *Handler) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Body.String(); got != "{\"status\":\"unavailable\"}" {
		t.Fatalf("body = %q", got)
	}
}

func readyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	keys, err := crypto.New([32]byte{1})
	if err != nil {
		t.Fatalf("create keys: %v", err)
	}
	if err := keys.EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("ensure sentinel: %v", err)
	}
	if err := adminauth.NewRepository(db, nil).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	if _, err := db.Exec("UPDATE admins SET must_change_password = 0 WHERE id = 1"); err != nil {
		t.Fatalf("clear forced password change: %v", err)
	}
	return db
}

func testKeys(t *testing.T) *crypto.KeySet {
	t.Helper()
	keys, err := crypto.New([32]byte{1})
	if err != nil {
		t.Fatalf("create keys: %v", err)
	}
	return keys
}
