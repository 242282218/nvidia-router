package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
)

func TestMigrationAddsModelContextLength(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repository := modelcatalog.NewRepository(db)
	if err := repository.SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "context-model", UpstreamID: "vendor/context", DisplayName: "Context", Kind: modelcatalog.KindChat,
		ReasoningStatus: modelcatalog.ReasoningStatusUnknown, ReasoningWireFormat: "none",
	}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var declared int
	if err := db.QueryRow("SELECT context_length FROM models WHERE public_id = ?", "context-model").Scan(&declared); err != nil {
		t.Fatalf("read context_length: %v", err)
	}
	if declared != 0 {
		t.Fatalf("context_length default = %d, want 0 (undeclared)", declared)
	}

	models, err := repository.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Migrations seed additional rows (e.g. the deepseek alias backfill); find
	// ours by public_id instead of assuming the list has exactly one entry.
	var seeded *modelcatalog.Model
	for index := range models {
		if models[index].PublicID == "context-model" {
			seeded = &models[index]
		}
	}
	if seeded == nil || seeded.ContextLength != 0 {
		t.Fatalf("repository list context_length = %+v, want 0", seeded)
	}

	length := 131072
	patchID := seeded.ID
	patched, _, err := repository.Patch(context.Background(), patchID, modelcatalog.Patch{ContextLength: &length}, time.Now().UTC())
	if err != nil {
		t.Fatalf("patch context_length: %v", err)
	}
	if patched.ContextLength != length {
		t.Fatalf("patched context_length = %d, want %d", patched.ContextLength, length)
	}

	negative := -1
	if _, _, err := repository.Patch(context.Background(), patchID, modelcatalog.Patch{ContextLength: &negative}, time.Now().UTC()); err == nil {
		t.Fatal("negative context_length should be rejected")
	}
}
