package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"nvidia-router/internal/clock"
)

const defaultAdminUsername = "admin"

var (
	ErrCurrentPasswordIncorrect = errors.New("current password is incorrect")
	ErrPasswordTooShort         = errors.New("new password must be at least 12 characters")
	ErrPasswordIsDefault        = errors.New("new password must not equal admin")
)

type Repository struct {
	db    *sql.DB
	clock clock.Clock
}

func NewRepository(db *sql.DB, source clock.Clock) *Repository {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Repository{db: db, clock: source}
}

// EnsureAdmin creates the initial forced-change administrator when none exists.
func (r *Repository) EnsureAdmin(ctx context.Context) error {
	var exists int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM admins LIMIT 1").Scan(&exists)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check existing admin: %w", err)
	}

	passwordHash, err := HashPassword(defaultAdminUsername)
	if err != nil {
		return fmt.Errorf("hash initial admin password: %w", err)
	}
	now := timestamp(r.clock.Now())
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO admins (id, username, password_hash, must_change_password, created_at, updated_at)
		VALUES (1, ?, ?, 1, ?, ?)
		ON CONFLICT DO NOTHING
	`, defaultAdminUsername, passwordHash, now, now); err != nil {
		return fmt.Errorf("insert initial admin: %w", err)
	}
	return nil
}

func (r *Repository) VerifyCredentials(ctx context.Context, username, password string) (bool, error) {
	var storedUsername, passwordHash string
	if err := r.db.QueryRowContext(ctx, "SELECT username, password_hash FROM admins WHERE id = 1").Scan(&storedUsername, &passwordHash); err != nil {
		return false, fmt.Errorf("load admin credentials: %w", err)
	}
	matched, err := VerifyPassword(password, passwordHash)
	if err != nil {
		return false, fmt.Errorf("verify admin password: %w", err)
	}
	return username == storedUsername && matched, nil
}

func (r *Repository) MustChangePassword(ctx context.Context) (bool, error) {
	var mustChange bool
	if err := r.db.QueryRowContext(ctx, "SELECT must_change_password FROM admins WHERE id = 1").Scan(&mustChange); err != nil {
		return false, fmt.Errorf("load admin password state: %w", err)
	}
	return mustChange, nil
}

// ChangePassword updates the administrator password and revokes other active sessions.
func (r *Repository) ChangePassword(ctx context.Context, currentPassword, newPassword, currentSessionID string) (returnErr error) {
	if err := validateNewPassword(newPassword); err != nil {
		return fmt.Errorf("validate new password: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			returnErr = fmt.Errorf("rollback password change transaction: %w", errors.Join(returnErr, rollbackErr))
		}
	}()

	var passwordHash string
	if err := tx.QueryRowContext(ctx, "SELECT password_hash FROM admins WHERE id = 1").Scan(&passwordHash); err != nil {
		return fmt.Errorf("load admin password: %w", err)
	}
	matched, err := VerifyPassword(currentPassword, passwordHash)
	if err != nil {
		return fmt.Errorf("verify current password: %w", err)
	}
	if !matched {
		return fmt.Errorf("verify current password: %w", ErrCurrentPasswordIncorrect)
	}

	newHash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	now := timestamp(r.clock.Now())
	if _, err := tx.ExecContext(ctx, `
		UPDATE admins
		SET password_hash = ?, must_change_password = 0, updated_at = ?
		WHERE id = 1
	`, newHash, now); err != nil {
		return fmt.Errorf("update admin password: %w", err)
	}
	if err := revokeOtherSessions(ctx, tx, currentSessionID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change transaction: %w", err)
	}
	committed = true
	return nil
}

// ResetPassword is reserved for offline recovery when no authenticated session is available.
func (r *Repository) ResetPassword(ctx context.Context, newPassword string) (returnErr error) {
	if err := validateNewPassword(newPassword); err != nil {
		return fmt.Errorf("validate new password: %w", err)
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			returnErr = fmt.Errorf("rollback password reset transaction: %w", errors.Join(returnErr, rollbackErr))
		}
	}()

	now := timestamp(r.clock.Now())
	result, err := tx.ExecContext(ctx, `
		UPDATE admins
		SET password_hash = ?, must_change_password = 0, updated_at = ?
		WHERE id = 1
	`, newHash, now)
	if err != nil {
		return fmt.Errorf("update admin password during reset: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count reset admin rows: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("update admin password during reset: %w", sql.ErrNoRows)
	}
	if err := revokeOtherSessions(ctx, tx, "", now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset transaction: %w", err)
	}
	committed = true
	return nil
}

func validateNewPassword(password string) error {
	if password == defaultAdminUsername {
		return ErrPasswordIsDefault
	}
	if utf8.RuneCountInString(password) < 12 {
		return ErrPasswordTooShort
	}
	return nil
}

func revokeOtherSessions(ctx context.Context, tx *sql.Tx, currentSessionID, now string) error {
	if currentSessionID == "" {
		if _, err := tx.ExecContext(ctx, "UPDATE admin_sessions SET revoked_at = ? WHERE revoked_at IS NULL", now); err != nil {
			return fmt.Errorf("revoke all admin sessions: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE admin_sessions SET revoked_at = ? WHERE revoked_at IS NULL AND id <> ?", now, currentSessionID); err != nil {
		return fmt.Errorf("revoke other admin sessions: %w", err)
	}
	return nil
}

func timestamp(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}
