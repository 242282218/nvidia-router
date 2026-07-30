package adminauth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
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

func TestEnsureAdminConcurrentInitializationCreatesOneStableAdmin(t *testing.T) {
	db := newTestDatabase(t)
	repository := NewRepository(db, fixedClock{now: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)})

	const callers = 8
	start := make(chan struct{})
	errs := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errs <- repository.EnsureAdmin(context.Background())
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent EnsureAdmin: %v", err)
		}
	}

	if count := adminCount(t, db); count != 1 {
		t.Fatalf("admin count = %d, want 1", count)
	}
	created := readAdmin(t, db)
	if matched, err := VerifyPassword("admin", created.passwordHash); err != nil || !matched {
		t.Fatalf("concurrently created admin verification = %t, %v", matched, err)
	}

	for range callers {
		if err := repository.EnsureAdmin(context.Background()); err != nil {
			t.Fatalf("subsequent EnsureAdmin: %v", err)
		}
	}
	assertAdminEqual(t, readAdmin(t, db), created)
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

func TestChangePasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	db := newTestDatabase(t)
	repository := NewRepository(db, fixedClock{now: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)})
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	insertSession(t, db, "keep", nil)
	insertSession(t, db, "revoke", nil)
	beforeAdmin := readAdmin(t, db)
	beforeSessions := sessionRevocations(t, db)
	if _, err := db.Exec(`
		CREATE TRIGGER reject_admin_session_revocation
		BEFORE UPDATE OF revoked_at ON admin_sessions
		BEGIN
			SELECT RAISE(ABORT, 'session revocation rejected');
		END
	`); err != nil {
		t.Fatalf("create session trigger: %v", err)
	}

	err := repository.ChangePassword(context.Background(), "admin", "a replacement password", "keep")
	if err == nil {
		t.Fatal("ChangePassword succeeded despite session revocation trigger")
	}
	if !strings.Contains(err.Error(), "revoke other admin sessions") {
		t.Fatalf("ChangePassword error lacks revocation context: %v", err)
	}
	assertAdminEqual(t, readAdmin(t, db), beforeAdmin)
	assertSessionRevocationsEqual(t, sessionRevocations(t, db), beforeSessions)
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
		want            error
		dontWant        error
	}{
		{name: "wrong current password", currentPassword: "wrong", newPassword: "a replacement password", want: ErrCurrentPasswordIncorrect},
		{name: "short password", currentPassword: "admin", newPassword: "too-short", want: ErrPasswordTooShort},
		{name: "four multi-byte characters", currentPassword: "admin", newPassword: "😀😀😀😀", want: ErrPasswordTooShort},
		{name: "default password", currentPassword: "admin", newPassword: "admin", want: ErrPasswordIsDefault, dontWant: ErrPasswordTooShort},
	} {
		t.Run(input.name, func(t *testing.T) {
			err := repository.ChangePassword(context.Background(), input.currentPassword, input.newPassword, "active")
			if err == nil {
				t.Fatal("ChangePassword succeeded")
			}
			if !errors.Is(err, input.want) {
				t.Fatalf("ChangePassword error does not match expected cause: %v", err)
			}
			if input.dontWant != nil && errors.Is(err, input.dontWant) {
				t.Fatal("ChangePassword returned a less-specific password error")
			}
			assertAdminEqual(t, readAdmin(t, db), before)
			if revokedAt := sessionRevokedAt(t, db, "active"); revokedAt != nil {
				t.Fatalf("failed ChangePassword revoked current session at %q", *revokedAt)
			}
		})
	}
}

func TestResetPasswordWithoutCurrentPasswordRevokesAllSessionsAndPreservesNVIDIASecrets(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 15, 0, 0, 0, time.UTC)
	repository := NewRepository(db, fixedClock{now: now})
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if err := repository.ChangePassword(context.Background(), "admin", "previous recovery password", ""); err != nil {
		t.Fatalf("set previous password: %v", err)
	}
	insertSession(t, db, "first-reset-session", nil)
	insertSession(t, db, "second-reset-session", nil)
	insertNVIDIASecretFixture(t, db)
	beforeCiphertext, beforeNonce, beforeFingerprint := readNVIDIASecretFixture(t, db)

	if err := repository.ResetPassword(context.Background(), "replacement recovery password"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	admin := readAdmin(t, db)
	if admin.mustChangePassword {
		t.Fatal("ResetPassword left must_change_password enabled")
	}
	if matched, err := VerifyPassword("previous recovery password", admin.passwordHash); err != nil || matched {
		t.Fatalf("previous password verification = %t, %v", matched, err)
	}
	if matched, err := VerifyPassword("replacement recovery password", admin.passwordHash); err != nil || !matched {
		t.Fatalf("replacement password verification = %t, %v", matched, err)
	}
	for _, id := range []string{"first-reset-session", "second-reset-session"} {
		if revokedAt := sessionRevokedAt(t, db, id); revokedAt == nil || *revokedAt != now.Format(time.RFC3339) {
			t.Fatalf("session %q revoked_at = %v, want %q", id, revokedAt, now.Format(time.RFC3339))
		}
	}
	afterCiphertext, afterNonce, afterFingerprint := readNVIDIASecretFixture(t, db)
	if !bytes.Equal(afterCiphertext, beforeCiphertext) ||
		!bytes.Equal(afterNonce, beforeNonce) ||
		!bytes.Equal(afterFingerprint, beforeFingerprint) {
		t.Fatal("ResetPassword changed NVIDIA encrypted fields")
	}
}

func TestResetPasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	db := newTestDatabase(t)
	repository := NewRepository(db, fixedClock{now: time.Date(2026, time.July, 29, 15, 0, 0, 0, time.UTC)})
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	insertSession(t, db, "reset-session", nil)
	beforeAdmin := readAdmin(t, db)
	if _, err := db.Exec(`
		CREATE TRIGGER reject_reset_session_revocation
		BEFORE UPDATE OF revoked_at ON admin_sessions
		BEGIN
			SELECT RAISE(ABORT, 'reset revocation rejected');
		END
	`); err != nil {
		t.Fatalf("create session trigger: %v", err)
	}

	err := repository.ResetPassword(context.Background(), "replacement recovery password")
	if err == nil || !strings.Contains(err.Error(), "revoke all admin sessions") {
		t.Fatalf("ResetPassword error = %v, want revocation context", err)
	}
	assertAdminEqual(t, readAdmin(t, db), beforeAdmin)
	if revokedAt := sessionRevokedAt(t, db, "reset-session"); revokedAt != nil {
		t.Fatalf("failed ResetPassword revoked session at %q", *revokedAt)
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

func adminCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM admins").Scan(&count); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	return count
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

func insertNVIDIASecretFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix,
			created_at, updated_at
		) VALUES (?, ?, ?, 'nvapi-', '-tail', '2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z')
	`, []byte("reset-ciphertext"), []byte("reset-nonce"), []byte("reset-fingerprint")); err != nil {
		t.Fatalf("insert NVIDIA secret fixture: %v", err)
	}
}

func readNVIDIASecretFixture(t *testing.T, db *sql.DB) ([]byte, []byte, []byte) {
	t.Helper()
	var ciphertext, nonce, fingerprint []byte
	if err := db.QueryRow("SELECT ciphertext, nonce, fingerprint FROM nvidia_keys LIMIT 1").Scan(&ciphertext, &nonce, &fingerprint); err != nil {
		t.Fatalf("read NVIDIA secret fixture: %v", err)
	}
	return ciphertext, nonce, fingerprint
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

func sessionRevocations(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query("SELECT id, revoked_at FROM admin_sessions ORDER BY id")
	if err != nil {
		t.Fatalf("query session revocations: %v", err)
	}
	defer rows.Close()

	revocations := make(map[string]string)
	for rows.Next() {
		var id string
		var revokedAt sql.NullString
		if err := rows.Scan(&id, &revokedAt); err != nil {
			t.Fatalf("scan session revocation: %v", err)
		}
		if revokedAt.Valid {
			revocations[id] = revokedAt.String
		} else {
			revocations[id] = ""
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session revocations: %v", err)
	}
	return revocations
}

func assertSessionRevocationsEqual(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("session count = %d, want %d", len(got), len(want))
	}
	for id, wantRevokedAt := range want {
		if gotRevokedAt, found := got[id]; !found || gotRevokedAt != wantRevokedAt {
			t.Fatalf("session %q revocation changed", id)
		}
	}
}
