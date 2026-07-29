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
	errCurrentPasswordIncorrect = errors.New("current password is incorrect")
	errPasswordTooShort         = errors.New("new password must be at least 12 characters")
	errPasswordIsDefault        = errors.New("new password must not equal admin")
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
		return fmt.Errorf("verify current password: %w", errCurrentPasswordIncorrect)
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

func validateNewPassword(password string) error {
	if utf8.RuneCountInString(password) < 12 {
		return errPasswordTooShort
	}
	if password == defaultAdminUsername {
		return errPasswordIsDefault
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
