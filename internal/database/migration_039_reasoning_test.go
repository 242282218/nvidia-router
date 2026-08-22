package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/runtimeconfig"
)

func TestLatestMigrationsAddReasoningConfiguration(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	settings, err := runtimeconfig.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Snapshot().AutoReasoningEnabled {
		t.Fatal("auto reasoning should default to enabled")
	}

	models, err := modelcatalog.NewRepository(db).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = models
	if err := modelcatalog.NewRepository(db).SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "opencodefree/test", UpstreamID: "test", DisplayName: "test",
		Kind: modelcatalog.KindChat, Provider: modelcatalog.ProviderOpenCodeFree,
		ReasoningStatus: modelcatalog.ReasoningStatusUnknown, ReasoningWireFormat: "none",
		ReasoningLevels: []string{"none"}, ReasoningMaxBudget: 128000,
		ReasoningZeroAllowed: true,
	}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var reasoningStatus string
	err = db.QueryRow("SELECT reasoning_status FROM models WHERE public_id = ?", "opencodefree/test").Scan(&reasoningStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read reasoning_status: %v", err)
	}
	if err == nil && reasoningStatus == "" {
		t.Fatal("reasoning_status should not be empty")
	}
}

func TestModelSelectionPreservesProbeReasoningVerdict(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	repository := modelcatalog.NewRepository(db)
	now := time.Now().UTC()
	verified := modelcatalog.Selection{
		PublicID: "opencodefree/preserved", UpstreamID: "preserved", DisplayName: "preserved",
		Kind: modelcatalog.KindChat, Provider: modelcatalog.ProviderOpenCodeFree,
		SupportsReasoning: true, ReasoningStatus: modelcatalog.ReasoningStatusVisible,
		ReasoningWireFormat: "openai", ReasoningLevels: []string{"none", "high"},
		ReasoningMaxBudget: 128000, ReasoningZeroAllowed: true,
	}
	if err := repository.SaveSelections(context.Background(), []modelcatalog.Selection{verified}, now); err != nil {
		t.Fatal(err)
	}
	static := verified
	static.SupportsReasoning = false
	static.ReasoningStatus = modelcatalog.ReasoningStatusUnknown
	static.ReasoningWireFormat = "none"
	static.ReasoningLevels = []string{"none"}
	if err := repository.SaveSelections(context.Background(), []modelcatalog.Selection{static}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var supported int
	var status, wire string
	if err := db.QueryRow("SELECT supports_reasoning, reasoning_status, reasoning_wire_format FROM models WHERE public_id = ?", verified.PublicID).Scan(&supported, &status, &wire); err != nil {
		t.Fatal(err)
	}
	if supported != 1 || status != modelcatalog.ReasoningStatusVisible || wire != "openai" {
		t.Fatalf("probe verdict overwritten: supported=%d status=%q wire=%q", supported, status, wire)
	}
}
