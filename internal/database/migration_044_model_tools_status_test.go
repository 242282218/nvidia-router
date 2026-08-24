package database

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration044MapsLegacyToolDeclarations(t *testing.T) {
	prior := migrationFixture(t, migrationsBefore(t, 44))
	with044 := addMigration(t, prior, "044_model_tools_status.sql")
	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-044: %v", err)
	}

	for _, model := range []struct {
		publicID     string
		supportsTool int
	}{
		{publicID: "legacy-tools-yes", supportsTool: 1},
		{publicID: "legacy-tools-no", supportsTool: 0},
	} {
		_, err := db.Exec(`
			INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, supports_tools, created_at, updated_at)
			VALUES (?, ?, ?, 'chat', 0, ?, '2026-08-24T00:00:00Z', '2026-08-24T00:00:00Z')
		`, model.publicID, "vendor/"+model.publicID, model.publicID, model.supportsTool)
		if err != nil {
			t.Fatalf("seed %s: %v", model.publicID, err)
		}
	}
	if err := migrateFS(db, with044); err != nil {
		t.Fatalf("migrate 044: %v", err)
	}

	assertToolStatus := func(publicID, wantStatus string) {
		t.Helper()
		var status string
		var verifiedAt sql.NullString
		if err := db.QueryRow("SELECT tools_status, tools_verified_at FROM models WHERE public_id = ?", publicID).Scan(&status, &verifiedAt); err != nil {
			t.Fatalf("read %s tool status: %v", publicID, err)
		}
		if status != wantStatus {
			t.Fatalf("%s tools_status = %q, want %q", publicID, status, wantStatus)
		}
		if verifiedAt.Valid {
			t.Fatalf("%s tools_verified_at = %q, want NULL", publicID, verifiedAt.String)
		}
	}

	assertToolStatus("legacy-tools-yes", "inferred")
	assertToolStatus("legacy-tools-no", "unknown")
	if _, err := db.Exec("UPDATE models SET tools_status = 'invalid' WHERE public_id = ?", "legacy-tools-yes"); err == nil {
		t.Fatal("invalid tools_status was accepted")
	}
}
