package adminauth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/database"
)

func TestEnsureAdminCreatesDefaultAdminOnlyOnce(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	repository := NewRepository(db, fixedClock{now: now})

	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	admin := readAdmin(t, db)
	if admin.username != "admin" || !admin.mustChangePassword {
		t.Fatalf("created admin username=%q must_change_password=%t, want admin and true", admin.username, admin.mustChangePassword)
	}
	if admin.createdAt != now.Format(time.RFC3339) || admin.updatedAt != now.Format(time.RFC3339) {
		t.Fatalf("created admin timestamps = %q, %q", admin.createdAt, admin.updatedAt)
	}
	matched, err := VerifyPassword("admin", admin.passwordHash)
	if err != nil || !matched {
		t.Fatalf("default admin password verification = %t, %v", matched, err)
	}

	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("second EnsureAdmin: %v", err)
	}
	assertAdminEqual(t, readAdmin(t, db), admin)
}

func TestEnsureAdminPreservesExistingAdmin(t *testing.T) {
	db := newTestDatabase(t)
	existing := adminRecord{
		username:           "admin",
		passwordHash:       "existing-hash",
		mustChangePassword: false,
		createdAt:          "2020-01-01T00:00:00Z",
		updatedAt:          "2020-01-01T00:00:00Z",
	}
	insertAdmin(t, db, existing)

	if err := NewRepository(db, fixedClock{}).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	assertAdminEqual(t, readAdmin(t, db), existing)
}

func TestChangePasswordUpdatesPasswordAndRevokesOtherSessions(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	repository := NewRepository(db, fixedClock{now: now})
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	insertSession(t, db, "keep", nil)
	insertSession(t, db, "revoke", nil)
	alreadyRevoked := "2026-07-01T00:00:00Z"
	insertSession(t, db, "already-revoked", &alreadyRevoked)

	if err := repository.ChangePassword(context.Background(), "admin", "a replacement password", "keep"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	admin := readAdmin(t, db)
	if admin.mustChangePassword {
		t.Fatal("ChangePassword left must_change_password enabled")
	}
	if admin.updatedAt != now.Format(time.RFC3339) {
		t.Fatalf("updated_at = %q, want %q", admin.updatedAt, now.Format(time.RFC3339))
	}
	if matched, err := VerifyPassword("admin", admin.passwordHash); err != nil || matched {
		t.Fatalf("old password verification = %t, %v", matched, err)
	}
	if matched, err := VerifyPassword("a replacement password", admin.passwordHash); err != nil || !matched {
		t.Fatalf("new password verification = %t, %v", matched, err)
	}
	if revokedAt := sessionRevokedAt(t, db, "keep"); revokedAt != nil {
		t.Fatalf("current session revoked_at = %q, want NULL", *revokedAt)
	}
	if revokedAt := sessionRevokedAt(t, db, "revoke"); revokedAt == nil || *revokedAt != now.Format(time.RFC3339) {
		t.Fatalf("other session revoked_at = %v, want %q", revokedAt, now.Format(time.RFC3339))
	}
	if revokedAt := sessionRevokedAt(t, db, "already-revoked"); revokedAt == nil || *revokedAt != alreadyRevoked {
		t.Fatalf("previously revoked session changed to %v", revokedAt)
	}
}

func TestChangePasswordWithoutCurrentSessionRevokesAllActiveSessions(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	repository := NewRepository(db, fixedClock{now: now})
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	insertSession(t, db, "first", nil)
	insertSession(t, db, "second", nil)

	if err := repository.ChangePassword(context.Background(), "admin", "a replacement password", ""); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	for _, id := range []string{"first", "second"} {
		if revokedAt := sessionRevokedAt(t, db, id); revokedAt == nil || *revokedAt != now.Format(time.RFC3339) {
			t.Fatalf("session %q revoked_at = %v, want %q", id, revokedAt, now.Format(time.RFC3339))
		}
	}
}

func TestChangePasswordRejectsInvalidCredentialsAndWeakNewPassword(t *testing.T) {
	db := newTestDatabase(t)
	repository := NewRepository(db, fixedClock{})
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	before := readAdmin(t, db)
	insertSession(t, db, "active", nil)

	for _, input := range []struct {
		name            string
		currentPassword string
		newPassword     string
	}{
		{name: "wrong current password", currentPassword: "wrong", newPassword: "a replacement password"},
		{name: "short password", currentPassword: "admin", newPassword: "too-short"},
		{name: "four multi-byte characters", currentPassword: "admin", newPassword: "😀😀😀😀"},
		{name: "default password", currentPassword: "admin", newPassword: "admin"},
	} {
		t.Run(input.name, func(t *testing.T) {
			if err := repository.ChangePassword(context.Background(), input.currentPassword, input.newPassword, "active"); err == nil {
				t.Fatal("ChangePassword succeeded")
			}
			assertAdminEqual(t, readAdmin(t, db), before)
			if revokedAt := sessionRevokedAt(t, db, "active"); revokedAt != nil {
				t.Fatalf("failed ChangePassword revoked current session at %q", *revokedAt)
			}
		})
	}
}

type fixedClock struct {
	clock.RealClock
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

type adminRecord struct {
	username           string
	passwordHash       string
	mustChangePassword bool
	createdAt          string
	updatedAt          string
}

func assertAdminEqual(t *testing.T, got, want adminRecord) {
	t.Helper()
	if got.username != want.username {
		t.Fatalf("admin username = %q, want %q", got.username, want.username)
	}
	if got.passwordHash != want.passwordHash {
		t.Fatal("admin password hash changed")
	}
	if got.mustChangePassword != want.mustChangePassword {
		t.Fatalf("admin must_change_password = %t, want %t", got.mustChangePassword, want.mustChangePassword)
	}
	if got.createdAt != want.createdAt || got.updatedAt != want.updatedAt {
		t.Fatal("admin timestamps changed")
	}
}

func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func readAdmin(t *testing.T, db *sql.DB) adminRecord {
	t.Helper()
	var record adminRecord
	if err := db.QueryRow(`
		SELECT username, password_hash, must_change_password, created_at, updated_at
		FROM admins WHERE id = 1
	`).Scan(&record.username, &record.passwordHash, &record.mustChangePassword, &record.createdAt, &record.updatedAt); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	return record
}

func insertAdmin(t *testing.T, db *sql.DB, record adminRecord) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO admins (id, username, password_hash, must_change_password, created_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?)
	`, record.username, record.passwordHash, record.mustChangePassword, record.createdAt, record.updatedAt); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
}

func insertSession(t *testing.T, db *sql.DB, id string, revokedAt *string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO admin_sessions (id, token_digest, expires_at, created_at, last_seen_at, revoked_at)
		VALUES (?, ?, '2030-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', ?)
	`, id, []byte(id), revokedAt); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func sessionRevokedAt(t *testing.T, db *sql.DB, id string) *string {
	t.Helper()
	var revokedAt sql.NullString
	if err := db.QueryRow("SELECT revoked_at FROM admin_sessions WHERE id = ?", id).Scan(&revokedAt); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !revokedAt.Valid {
		return nil
	}
	return &revokedAt.String
}
