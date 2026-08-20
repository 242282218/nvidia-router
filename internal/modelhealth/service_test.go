package modelhealth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/modelcatalog"
)

func TestServiceRunOnceProbesEveryWhitelistedModel(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 101, "nvidia-model")
	insertModelHealthModel(t, db, 102, "free-model")
	if _, err := db.Exec("UPDATE models SET provider = 'opencodefree' WHERE id = 102"); err != nil {
		t.Fatalf("set test provider: %v", err)
	}
	repository := NewRepository(db)
	catalog := &modelHealthCatalogFake{models: []modelcatalog.Model{
		{ID: 101, PublicID: "nvidia-model", Provider: modelcatalog.ProviderNVIDIA},
		{ID: 102, PublicID: "free-model", Provider: modelcatalog.ProviderOpenCodeFree},
	}}
	prober := &modelHealthProberFake{failures: map[int64]error{102: errors.New("upstream failure")}}
	keys := &modelHealthKeyFake{id: 41}
	service := NewService(repository, catalog, prober, keys, clock.RealClock{}, slog.Default())

	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(prober.calls()) != 2 {
		t.Fatalf("probe calls = %+v, want both models", prober.calls())
	}
	if got := prober.keyFor(101); got != 41 {
		t.Fatalf("NVIDIA key = %d, want 41", got)
	}
	if got := prober.keyFor(102); got != 0 {
		t.Fatalf("OpenCodeFree key = %d, want 0", got)
	}
	events, err := repository.ListEvents(context.Background(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want one per model", events)
	}
}

func TestServiceSummaryKeepsUncheckedModelAndClassifiesFailure(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 101, "healthy-model")
	insertModelHealthModel(t, db, 102, "unchecked-model")
	repository := NewRepository(db)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	catalog := &modelHealthCatalogFake{models: []modelcatalog.Model{
		{ID: 101, PublicID: "healthy-model", DisplayName: "Healthy", Provider: modelcatalog.ProviderNVIDIA},
		{ID: 102, PublicID: "unchecked-model", DisplayName: "Unchecked", Provider: modelcatalog.ProviderNVIDIA},
	}}
	service := NewService(repository, catalog, &modelHealthProberFake{}, &modelHealthKeyFake{id: 1}, fixedModelHealthClock{now: now}, slog.Default())
	if err := repository.Record(context.Background(), ProbeEvent{ModelID: 101, Outcome: OutcomeFailure, DurationMS: 22, CreatedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	summary, err := service.Summary(context.Background(), "1h", "availability")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(summary.Models) != 2 {
		t.Fatalf("summary models = %d, want 2", len(summary.Models))
	}
	if summary.Models[0].Status != StatusUnavailable {
		t.Fatalf("first model status = %q, want unavailable", summary.Models[0].Status)
	}
	if summary.Models[1].Status != StatusUnchecked {
		t.Fatalf("second model status = %q, want unchecked", summary.Models[1].Status)
	}
}

func TestServiceSummaryCountsStaleSeparatelyFromUnchecked(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 301, "stale-model")
	insertModelHealthModel(t, db, 302, "unchecked-model")
	repository := NewRepository(db)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	catalog := &modelHealthCatalogFake{models: []modelcatalog.Model{
		{ID: 301, PublicID: "stale-model", Provider: modelcatalog.ProviderNVIDIA},
		{ID: 302, PublicID: "unchecked-model", Provider: modelcatalog.ProviderNVIDIA},
	}}
	service := NewService(repository, catalog, &modelHealthProberFake{}, &modelHealthKeyFake{id: 1}, fixedModelHealthClock{now: now}, slog.Default())
	if err := repository.Record(context.Background(), ProbeEvent{
		ModelID: 301, Outcome: OutcomeSuccess, DurationMS: 20, CreatedAt: now.Add(-3 * time.Minute),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	summary, err := service.Summary(context.Background(), "1h", "availability")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Stale != 1 || summary.Unchecked != 1 {
		t.Fatalf("status counts = stale %d/unchecked %d, want 1/1", summary.Stale, summary.Unchecked)
	}
}

func TestServiceProbeRecordsCanceledOutcomeWhenContextIsCanceled(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 303, "canceled-model")
	repository := NewRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewService(
		repository,
		&modelHealthCatalogFake{models: []modelcatalog.Model{{ID: 303, PublicID: "canceled-model", Provider: modelcatalog.ProviderNVIDIA}}},
		&modelHealthProberFake{failures: map[int64]error{303: context.Canceled}},
		&modelHealthKeyFake{id: 1},
		clock.RealClock{},
		slog.Default(),
	)

	if err := service.probeOne(ctx, modelcatalog.Model{ID: 303, PublicID: "canceled-model", Provider: modelcatalog.ProviderNVIDIA}, 1, nil); err != nil {
		t.Fatalf("probeOne: %v", err)
	}
	events, err := repository.ListEvents(context.Background(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Outcome != OutcomeCanceled || events[0].ErrorCode != "canceled" {
		t.Fatalf("canceled events = %+v, want one canceled event", events)
	}
}

func TestServiceEnablingSchedulerTriggersFirstProbeImmediately(t *testing.T) {
	db := openModelHealthTestDB(t)
	insertModelHealthModel(t, db, 201, "scheduled-model")
	repository := NewRepository(db)
	prober := &modelHealthSignalProber{called: make(chan struct{}, 1)}
	service := NewService(
		repository,
		&modelHealthCatalogFake{models: []modelcatalog.Model{{ID: 201, PublicID: "scheduled-model", Provider: modelcatalog.ProviderNVIDIA}}},
		prober,
		&modelHealthKeyFake{id: 1},
		clock.RealClock{},
		slog.Default(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() {
		cancel()
		<-done
	}()

	if _, err := service.UpdateSettings(ctx, Settings{Enabled: true, IntervalSeconds: 10, Concurrency: 1}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	select {
	case <-prober.called:
	case <-time.After(time.Second):
		t.Fatal("enabling scheduler did not trigger an immediate probe")
	}
}

type modelHealthCatalogFake struct {
	models []modelcatalog.Model
}

func (f *modelHealthCatalogFake) List(context.Context) ([]modelcatalog.Model, error) {
	return append([]modelcatalog.Model(nil), f.models...), nil
}

type modelHealthProberFake struct {
	mu       sync.Mutex
	failures map[int64]error
	seen     map[int64]int64
}

type modelHealthProbeCall struct {
	modelID int64
	keyID   int64
}

func (f *modelHealthProberFake) TestModel(_ context.Context, _ string, keyID, modelID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = make(map[int64]int64)
	}
	f.seen[modelID] = keyID
	return f.failures[modelID]
}

func (f *modelHealthProberFake) calls() []modelHealthProbeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]modelHealthProbeCall, 0, len(f.seen))
	for modelID, keyID := range f.seen {
		calls = append(calls, modelHealthProbeCall{modelID: modelID, keyID: keyID})
	}
	return calls
}

func (f *modelHealthProberFake) keyFor(modelID int64) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[modelID]
}

type modelHealthKeyFake struct {
	id  int64
	err error
}

type modelHealthSignalProber struct {
	called chan struct{}
}

func (f *modelHealthSignalProber) TestModel(context.Context, string, int64, int64) error {
	select {
	case f.called <- struct{}{}:
	default:
	}
	return nil
}

func (f *modelHealthKeyFake) FirstEnabledID(context.Context) (int64, error) {
	return f.id, f.err
}

type fixedModelHealthClock struct {
	now time.Time
}

func (f fixedModelHealthClock) Now() time.Time { return f.now }

func (fixedModelHealthClock) NewTimer(duration time.Duration) *time.Timer {
	return time.NewTimer(duration)
}

func (fixedModelHealthClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}
