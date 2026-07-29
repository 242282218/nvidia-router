package database

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const (
	journalModeWAL  = "wal"
	foreignKeysOn   = 1
	busyTimeoutMS   = 5000
	synchronousMode = 1
)

func Open(path string) (*sql.DB, error) {
	dsn := "file:" + url.PathEscape(path) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := configure(db); err != nil {
		return nil, closeAfterError(db, "configure SQLite database", err)
	}
	if err := Migrate(db); err != nil {
		return nil, closeAfterError(db, "migrate SQLite database", err)
	}
	return db, nil
}

func closeAfterError(db *sql.DB, operation string, operationErr error) error {
	if closeErr := db.Close(); closeErr != nil {
		return fmt.Errorf("%s and close SQLite database: %w", operation, errors.Join(operationErr, closeErr))
	}
	return fmt.Errorf("%s: %w", operation, operationErr)
}

func configure(db *sql.DB) error {
	if err := execAndVerifyText(db, "PRAGMA journal_mode = WAL", "PRAGMA journal_mode", journalModeWAL); err != nil {
		return fmt.Errorf("enable SQLite WAL: %w", err)
	}
	if err := execAndVerifyInt(db, "PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", foreignKeysOn); err != nil {
		return fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	if err := execAndVerifyInt(db, "PRAGMA busy_timeout = 5000", "PRAGMA busy_timeout", busyTimeoutMS); err != nil {
		return fmt.Errorf("configure SQLite busy timeout: %w", err)
	}
	if err := execAndVerifyInt(db, "PRAGMA synchronous = NORMAL", "PRAGMA synchronous", synchronousMode); err != nil {
		return fmt.Errorf("configure SQLite synchronous mode: %w", err)
	}
	return nil
}

func execAndVerifyText(db *sql.DB, statement, query, want string) error {
	var got string
	if err := db.QueryRow(statement).Scan(&got); err != nil {
		return fmt.Errorf("execute %q: %w", statement, err)
	}
	if err := db.QueryRow(query).Scan(&got); err != nil {
		return fmt.Errorf("verify %q: %w", statement, err)
	}
	if got != want {
		return fmt.Errorf("verify %q: %w", statement, fmt.Errorf("got %q, want %q", got, want))
	}
	return nil
}

func execAndVerifyInt(db *sql.DB, statement, query string, want int) error {
	if _, err := db.Exec(statement); err != nil {
		return fmt.Errorf("execute %q: %w", statement, err)
	}

	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		return fmt.Errorf("verify %q: %w", statement, err)
	}
	if got != want {
		return fmt.Errorf("verify %q: %w", statement, fmt.Errorf("got %d, want %d", got, want))
	}
	return nil
}
