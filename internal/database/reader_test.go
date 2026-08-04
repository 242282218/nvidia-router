package database

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpenReaderReadsWriterCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = writer.Close() }()

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = reader.Close() }()

	ctx := context.Background()
	if _, err := writer.ExecContext(ctx, `INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES (?, ?, ?, ?)`,
		"probe", []byte("digest-a"), "nvr_probe000", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert through writer: %v", err)
	}

	var name string
	if err := reader.QueryRowContext(ctx, `SELECT name FROM access_keys WHERE key_digest = ?`, []byte("digest-a")).Scan(&name); err != nil {
		t.Fatalf("read through reader: %v", err)
	}
	if name != "probe" {
		t.Fatalf("reader saw name %q, want %q", name, "probe")
	}
}

func TestOpenReaderRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = writer.Close() }()

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = reader.Close() }()

	_, err = reader.ExecContext(context.Background(),
		`INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES (?, ?, ?, ?)`,
		"nope", []byte("digest-b"), "nvr_nope0000", "2026-01-01T00:00:00Z")
	if err == nil {
		t.Fatal("reader pool accepted a write; mode=ro is not in effect")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Fatalf("write through reader failed with unexpected error: %v", err)
	}
}

func TestOpenReaderServesConcurrentReadsDuringWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = writer.Close() }()

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = reader.Close() }()

	ctx := context.Background()
	if _, err := writer.ExecContext(ctx, `INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES (?, ?, ?, ?)`,
		"probe", []byte("digest-c"), "nvr_probe000", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	// Hold an open write transaction, then prove reads still complete. On the
	// single writer pool this would deadlock against the in-flight transaction.
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin write transaction: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES (?, ?, ?, ?)`,
		"pending", []byte("digest-d"), "nvr_pend0000", "2026-01-01T00:00:00Z"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert inside transaction: %v", err)
	}

	var group sync.WaitGroup
	errs := make(chan error, ReaderMaxConns)
	for range ReaderMaxConns {
		group.Add(1)
		go func() {
			defer group.Done()
			var count int
			if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_keys`).Scan(&count); err != nil {
				errs <- err
				return
			}
			// Uncommitted row must not be visible.
			if count != 1 {
				errs <- errUnexpectedCount(count)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		_ = tx.Rollback()
		t.Fatalf("concurrent read during open write transaction: %v", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

type countError int

func (e countError) Error() string {
	return "unexpected visible row count during open write transaction"
}

func errUnexpectedCount(count int) error { return countError(count) }
