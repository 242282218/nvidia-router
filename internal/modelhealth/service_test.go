package modelhealth

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/modelcatalog"
)

type deletingModelSource struct {
	models []modelcatalog.Model
}

func (source deletingModelSource) List(context.Context) ([]modelcatalog.Model, error) {
	return source.models, nil
}

type deletingModelProber struct {
	db    *sql.DB
	model int64
}

func (prober deletingModelProber) TestModel(context.Context, string, int64, int64) error {
	_, err := prober.db.Exec("DELETE FROM models WHERE id = ?", prober.model)
	return err
}

type successfulModelProber struct{}

func (successfulModelProber) TestModel(context.Context, string, int64, int64) error {
	return nil
}

func TestRunOnceIgnoresModelDeletedDuringProbe(t *testing.T) {
	db := openModelHealthTestDB(t)
	modelID := int64(7)
	insertModelHealthModel(t, db, modelID, "deleted-during-probe")
	model := modelcatalog.Model{ID: modelID, PublicID: "opencodefree/deleted-during-probe", Provider: modelcatalog.ProviderOpenCodeFree}
	service := NewService(
		NewRepository(db),
		deletingModelSource{models: []modelcatalog.Model{model}},
		deletingModelProber{db: db, model: modelID},
		nil,
		clock.RealClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM models WHERE id = ?", modelID).Scan(&remaining); err != nil {
		t.Fatalf("count deleted model: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("deleted model rows = %d, want 0", remaining)
	}
}

func TestRunOncePropagatesUnexpectedRecordError(t *testing.T) {
	db := openModelHealthTestDB(t)
	modelID := int64(7)
	insertModelHealthModel(t, db, modelID, "record-error")
	if _, err := db.Exec("DROP TABLE model_health_probes"); err != nil {
		t.Fatalf("drop model health probes: %v", err)
	}
	model := modelcatalog.Model{ID: modelID, PublicID: "opencodefree/record-error", Provider: modelcatalog.ProviderOpenCodeFree}
	service := NewService(
		NewRepository(db),
		deletingModelSource{models: []modelcatalog.Model{model}},
		successfulModelProber{},
		nil,
		clock.RealClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	if err := service.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce swallowed an unexpected model health record error")
	}
}

func TestClassifyStatusDistinguishesKeyCoolingFromUnconfigured(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	interval := time.Minute

	// Case 1: keys exist (count=2) but all are cooling (FirstEnabledID returns sql.ErrNoRows)
	latest := &Latest{
		Outcome:     OutcomeSkipped,
		ErrorCode:   "keys_cooling",
		LastProbeAt: now,
	}
	stats := WindowStats{ProbeCount: 1}
	status := ClassifyStatus(latest, stats, now, interval)
	if status == StatusUnconfigured {
		t.Errorf("ClassifyStatus with keys_cooling = %v; want != StatusUnconfigured", status)
	}

	// Case 2: truly no keys configured
	latest = &Latest{
		Outcome:     OutcomeSkipped,
		ErrorCode:   "no_credential",
		LastProbeAt: now,
	}
	status = ClassifyStatus(latest, stats, now, interval)
	if status != StatusUnconfigured {
		t.Errorf("ClassifyStatus with no_credential = %v; want StatusUnconfigured", status)
	}
}
