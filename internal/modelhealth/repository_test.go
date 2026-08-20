package modelhealth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/database"
)

func TestRepositorySettingsRoundTrip(t *testing.T) {
	db := openModelHealthTestDB(t)
	repository := NewRepository(db)

	got, err := repository.LoadSettings(context.Background())
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.IntervalSeconds != DefaultIntervalSeconds || got.Concurrency != DefaultConcurrency || got.Enabled {
		t.Fatalf("default settings = %+v", got)
	}

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	updated := Settings{Enabled: true, IntervalSeconds: 300, Concurrency: 4}
	if got, err = repository.SaveSettings(context.Background(), updated, now); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if !got.Enabled || got.IntervalSeconds != 300 || got.Concurrency != 4 || !got.UpdatedAt.Equal(now) {
		t.Fatalf("saved settings = %+v", got)
	}

	loaded, err := repository.LoadSettings(context.Background())
	if err != nil {
		t.Fatalf("LoadSettings after save: %v", err)
	}
	if loaded != got {
		t.Fatalf("loaded settings = %+v, want %+v", loaded, got)
	}
}

func TestRepositoryPatchSettingsSerializesPartialUpdates(t *testing.T) {
	db := openModelHealthTestDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	patches := []SettingsPatch{
		{Enabled: boolPointer(true)},
		{IntervalSeconds: intPointer(300)},
	}
	errors := make(chan error, len(patches))
	for _, patch := range patches {
		go func(patch SettingsPatch) {
			_, err := repository.PatchSettings(context.Background(), patch, now)
			errors <- err
		}(patch)
	}
	for range patches {
		if err := <-errors; err != nil {
			t.Fatalf("PatchSettings: %v", err)
		}
	}

	settings, err := repository.LoadSettings(context.Background())
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !settings.Enabled || settings.IntervalSeconds != 300 || settings.Concurrency != DefaultConcurrency {
		t.Fatalf("settings after concurrent patches = %+v, want enabled/300/%d", settings, DefaultConcurrency)
	}
}

func TestRepositorySummarySnapshotReadsAllHealthState(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 7, "snapshot-model")
	repository := NewRepository(db)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if err := repository.Record(context.Background(), ProbeEvent{
		ModelID: 7, Outcome: OutcomeSuccess, DurationMS: 42, CreatedAt: now,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	snapshot, err := repository.SummarySnapshot(context.Background(), now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("SummarySnapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].ModelID != 7 {
		t.Fatalf("snapshot events = %+v, want one event for model 7", snapshot.Events)
	}
	if snapshot.Latest[7].Outcome != OutcomeSuccess {
		t.Fatalf("snapshot latest = %+v, want success", snapshot.Latest[7])
	}
	if snapshot.Settings.IntervalSeconds != DefaultIntervalSeconds {
		t.Fatalf("snapshot settings = %+v, want default interval", snapshot.Settings)
	}
}

func TestRepositoryRecordUpdatesLatestAndListsHistory(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 7, "model-7")
	repository := NewRepository(db)
	base := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	for index, outcome := range []string{OutcomeFailure, OutcomeTimeout, OutcomeSuccess} {
		event := ProbeEvent{
			ModelID: 7, Outcome: outcome, DurationMS: int64(index + 1),
			ErrorCode: map[string]string{OutcomeFailure: "probe_failed", OutcomeTimeout: "timeout"}[outcome],
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}
		if err := repository.Record(context.Background(), event); err != nil {
			t.Fatalf("Record(%s): %v", outcome, err)
		}
	}

	latest, err := repository.ListLatest(context.Background())
	if err != nil {
		t.Fatalf("ListLatest: %v", err)
	}
	if latest[7].Outcome != OutcomeSuccess || latest[7].ConsecutiveFailures != 0 {
		t.Fatalf("latest[7] = %+v, want success/reset", latest[7])
	}

	events, err := repository.ListEvents(context.Background(), base.Add(-time.Second), base.Add(3*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 || events[0].Outcome != OutcomeFailure || events[2].Outcome != OutcomeSuccess {
		t.Fatalf("events = %+v, want chronological three events", events)
	}
}

func TestRepositoryRejectsUnknownModelAndInvalidEvent(t *testing.T) {
	db := openModelHealthTestDB(t)
	repository := NewRepository(db)
	err := repository.Record(context.Background(), ProbeEvent{ModelID: 999, Outcome: OutcomeSuccess, CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("Record unknown model succeeded")
	}
	err = repository.Record(context.Background(), ProbeEvent{ModelID: 1, Outcome: "secret-response", CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("Record unknown outcome succeeded")
	}
}

func openModelHealthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertModelHealthModel(t *testing.T, db *sql.DB, id int64, publicID string) {
	t.Helper()
	now := "2026-08-20T12:00:00Z"
	_, err := db.Exec(`
		INSERT INTO models (id, public_id, upstream_id, display_name, kind, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'chat', 1, ?, ?)
	`, id, publicID, publicID, publicID, now, now)
	if err != nil {
		t.Fatalf("insert model: %v", err)
	}
}

func boolPointer(value bool) *bool { return &value }

func intPointer(value int) *int { return &value }
