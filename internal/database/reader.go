package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"runtime"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
)

// ReaderMaxConns bounds the read-only pool. WAL lets readers run concurrently
// with the single writer, so the only cost of extra connections is memory for
// each connection's page cache.
const ReaderMaxConns = 4

// OpenReader opens a second, read-only pool against an already-migrated
// database. The writer pool keeps SetMaxOpenConns(1) so every write stays
// serialized, but that also serialized reads behind pending writes; read-heavy
// paths (access-key authentication on every /v1 request, monitoring
// aggregations, health probes) use this pool instead.
//
// mode=ro is a hard guarantee rather than a convention: even a coding mistake
// cannot take a write lock through this handle. It requires the writer to have
// created the -shm file first, which Open does, because a read-only connection
// cannot create WAL index files itself.
func OpenReader(path string) (*sql.DB, error) {
	dsn := "file:" + url.PathEscape(path) + "?_timefmt=rfc3339&mode=ro"
	db, err := driver.Open(dsn, configureReaderConnection)
	if err != nil {
		return nil, fmt.Errorf("open SQLite reader pool: %w", err)
	}
	maxConns := ReaderMaxConns
	if cpus := runtime.NumCPU(); cpus < maxConns {
		maxConns = cpus
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	if err := db.Ping(); err != nil {
		return nil, closeAfterError(db, "configure SQLite reader pool", err)
	}
	return db, nil
}

func configureReaderConnection(conn *sqlite3.Conn) error {
	if err := execAndVerifyConnectionInt(conn, "PRAGMA busy_timeout = 5000", "PRAGMA busy_timeout", busyTimeoutMS); err != nil {
		return fmt.Errorf("configure SQLite reader busy timeout: %w", err)
	}
	// journal_mode is a database-level property already set by the writer, and a
	// read-only connection cannot change it. Verify rather than set, so a
	// non-WAL database fails loudly instead of silently serializing readers.
	mode, err := connectionPragmaText(conn, "PRAGMA journal_mode")
	if err != nil {
		return fmt.Errorf("verify SQLite reader journal mode: %w", err)
	}
	if mode != journalModeWAL {
		return fmt.Errorf("verify SQLite reader journal mode: got %q, want %q", mode, journalModeWAL)
	}
	return nil
}
