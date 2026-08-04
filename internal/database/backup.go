package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	sqlite3driver "github.com/ncruces/go-sqlite3/driver"
)

func Backup(ctx context.Context, db *sql.DB, destination string) (returnErr error) {
	temporaryPath, destinationPath, err := createBackupFile(destination)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if published {
			return
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			returnErr = fmt.Errorf("remove incomplete SQLite backup: %w", errors.Join(returnErr, removeErr))
		}
	}()

	if err := copySQLiteDatabase(ctx, db, temporaryPath); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("set SQLite backup permissions: %w", err)
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		// os.Rename is not guaranteed to replace an existing target on every
		// platform (Windows can refuse when the destination exists or is held
		// by another process), while the backup CLI must be able to refresh an
		// existing --output file. Fall back to delete-then-rename; the small
		// non-atomic window is acceptable for a backup artifact, and a target
		// that cannot be removed surfaces as a clear error below.
		if removeErr := os.Remove(destinationPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("replace existing SQLite backup %q: %w", destinationPath, errors.Join(err, removeErr))
		}
		if err := os.Rename(temporaryPath, destinationPath); err != nil {
			return fmt.Errorf("publish SQLite backup: %w", err)
		}
	}
	published = true
	return nil
}

func createBackupFile(destination string) (string, string, error) {
	if destination == "" {
		return "", "", errors.New("SQLite backup destination is required")
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return "", "", fmt.Errorf("resolve SQLite backup destination: %w", err)
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(destinationPath), "."+filepath.Base(destinationPath)+".*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("create temporary SQLite backup: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	if err := temporaryFile.Chmod(0o600); err != nil {
		return "", "", closeAndRemoveBackupFile(temporaryFile, fmt.Errorf("set temporary SQLite backup permissions: %w", err))
	}
	if err := temporaryFile.Close(); err != nil {
		return "", "", closeAndRemoveBackupFile(temporaryFile, fmt.Errorf("close temporary SQLite backup: %w", err))
	}
	return temporaryPath, destinationPath, nil
}

func closeAndRemoveBackupFile(file *os.File, operationErr error) error {
	closeErr := file.Close()
	removeErr := os.Remove(file.Name())
	return fmt.Errorf("prepare temporary SQLite backup: %w", errors.Join(operationErr, closeErr, removeErr))
}

func copySQLiteDatabase(ctx context.Context, db *sql.DB, destinationPath string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite backup connection: %w", err)
	}
	backupErr := conn.Raw(func(driverConn any) error {
		rawConn, ok := driverConn.(sqlite3driver.Conn)
		if !ok {
			return errors.New("unexpected SQLite driver connection")
		}
		destinationURI := "file:" + url.PathEscape(destinationPath)
		if err := rawConn.Raw().Backup("main", destinationURI); err != nil {
			return fmt.Errorf("copy SQLite database: %w", err)
		}
		return nil
	})
	closeErr := conn.Close()
	if backupErr != nil || closeErr != nil {
		return fmt.Errorf("run SQLite online backup: %w", errors.Join(backupErr, closeErr))
	}
	return nil
}
