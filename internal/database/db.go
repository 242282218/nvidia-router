package database

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"github.com/ncruces/go-sqlite3"
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
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
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

func configureConnection(conn *sqlite3.Conn) error {
	if err := execAndVerifyConnectionText(conn, "PRAGMA journal_mode = WAL", "PRAGMA journal_mode", journalModeWAL); err != nil {
		return fmt.Errorf("enable SQLite WAL: %w", err)
	}
	if err := execAndVerifyConnectionInt(conn, "PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", foreignKeysOn); err != nil {
		return fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	if err := execAndVerifyConnectionInt(conn, "PRAGMA busy_timeout = 5000", "PRAGMA busy_timeout", busyTimeoutMS); err != nil {
		return fmt.Errorf("configure SQLite busy timeout: %w", err)
	}
	if err := execAndVerifyConnectionInt(conn, "PRAGMA synchronous = NORMAL", "PRAGMA synchronous", synchronousMode); err != nil {
		return fmt.Errorf("configure SQLite synchronous mode: %w", err)
	}
	return nil
}

func execAndVerifyConnectionText(conn *sqlite3.Conn, statement, query, want string) error {
	if err := conn.Exec(statement); err != nil {
		return fmt.Errorf("execute %q: %w", statement, err)
	}
	got, err := connectionPragmaText(conn, query)
	if err != nil {
		return fmt.Errorf("verify %q: %w", statement, err)
	}
	if got != want {
		return fmt.Errorf("verify %q: %w", statement, fmt.Errorf("got %q, want %q", got, want))
	}
	return nil
}

func execAndVerifyConnectionInt(conn *sqlite3.Conn, statement, query string, want int) error {
	if err := conn.Exec(statement); err != nil {
		return fmt.Errorf("execute %q: %w", statement, err)
	}
	got, err := connectionPragmaInt(conn, query)
	if err != nil {
		return fmt.Errorf("verify %q: %w", statement, err)
	}
	if got != want {
		return fmt.Errorf("verify %q: %w", statement, fmt.Errorf("got %d, want %d", got, want))
	}
	return nil
}

func connectionPragmaText(conn *sqlite3.Conn, query string) (string, error) {
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return "", fmt.Errorf("prepare %q: %w", query, err)
	}
	if !stmt.Step() {
		return "", closeFailedPragmaStatement(stmt, query, stmt.Err())
	}
	value := stmt.ColumnText(0)
	if err := stmt.Close(); err != nil {
		return "", fmt.Errorf("close %q: %w", query, err)
	}
	return value, nil
}

func connectionPragmaInt(conn *sqlite3.Conn, query string) (int, error) {
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return 0, fmt.Errorf("prepare %q: %w", query, err)
	}
	if !stmt.Step() {
		return 0, closeFailedPragmaStatement(stmt, query, stmt.Err())
	}
	value := stmt.ColumnInt(0)
	if err := stmt.Close(); err != nil {
		return 0, fmt.Errorf("close %q: %w", query, err)
	}
	return value, nil
}

func closeFailedPragmaStatement(stmt *sqlite3.Stmt, query string, stepErr error) error {
	if closeErr := stmt.Close(); closeErr != nil {
		return fmt.Errorf("read and close %q: %w", query, errors.Join(stepErr, closeErr))
	}
	return fmt.Errorf("read %q: %w", query, stepErr)
}
