package database

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrateRejectsChangedChecksum(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	err = migrateFS(db, fstest.MapFS{
		"migrations/001_initial.sql": &fstest.MapFile{Data: []byte("CREATE TABLE checksum_tampered (id INTEGER PRIMARY KEY);")},
		"migrations/003_later.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE must_not_exist (id INTEGER PRIMARY KEY);")},
	})
	if err == nil {
		t.Fatal("migrateFS succeeded after migration checksum changed")
	}
	for _, part := range []string{"migration", "version 1", "checksum"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("migrateFS error = %q, want substring %q", err, part)
		}
	}

	var count int
	query := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'must_not_exist'"
	if queryErr := db.QueryRow(query).Scan(&count); queryErr != nil {
		t.Fatalf("query later migration table: %v", queryErr)
	}
	if count != 0 {
		t.Fatal("later migration ran after checksum mismatch")
	}
}
