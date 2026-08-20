package modelcatalog

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
)

// probeService prepares one chat model and returns everything a probe test needs.
func probeService(t *testing.T, publicID string, availableKeys ...int64) (*Service, *fakeSecrets, *fakeDiscoverer, int64) {
	t.Helper()
	service, db, secrets, discoverer := newCatalogTestService(t)
	if err := service.SaveSelection(context.Background(), []Selection{{
		PublicID: publicID, UpstreamID: "vendor/" + publicID, DisplayName: publicID, Kind: KindChat, Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	secrets.availableIDs = availableKeys
	discoverer.chatResponse = `{"choices":[{"message":{"content":"ok"}}]}`
	return service, secrets, discoverer, modelIDByPublicID(t, db, publicID)
}

// The operator no longer picks a credential, so a probe that hits a bad key must
// move on to another one instead of reporting the model as broken.
func TestModelAutoRotatesNVIDIAKeysUntilOneAnswers(t *testing.T) {
	service, secrets, discoverer, modelID := probeService(t, "rotate-chat", 11, 12, 13)
	discoverer.chatStatuses = []int{503}

	if err := service.TestModelAuto(context.Background(), modelID); err != nil {
		t.Fatalf("TestModelAuto: %v", err)
	}
	if len(secrets.usedKeyIDs) != 2 {
		t.Fatalf("keys tried = %v, want two (one failure then a success)", secrets.usedKeyIDs)
	}
	if secrets.usedKeyIDs[0] == secrets.usedKeyIDs[1] {
		t.Fatalf("retry reused key %d instead of switching", secrets.usedKeyIDs[0])
	}
}

// A 404 describes the model, not the credential: another key would answer the
// same way, so burning the remaining keys would only waste upstream quota.
func TestModelAutoStopsOnAnAnswerThatDescribesTheModel(t *testing.T) {
	service, secrets, discoverer, modelID := probeService(t, "missing-chat", 11, 12, 13)
	discoverer.chatStatuses = []int{404}

	err := service.TestModelAuto(context.Background(), modelID)
	if !errors.Is(err, ErrManualTestRequired) || errors.Is(err, ErrUpstreamUnreachable) {
		t.Fatalf("error = %v, want a plain ErrManualTestRequired", err)
	}
	if len(secrets.usedKeyIDs) != 1 {
		t.Fatalf("keys tried = %v, want exactly one", secrets.usedKeyIDs)
	}
}

func TestModelAutoBoundsKeyRotationToProbeAttempts(t *testing.T) {
	service, secrets, discoverer, modelID := probeService(t, "dead-chat", 11, 12, 13, 14, 15)
	discoverer.chatStatuses = []int{503, 503, 503, 503, 503}

	if err := service.TestModelAuto(context.Background(), modelID); !errors.Is(err, ErrUpstreamUnreachable) {
		t.Fatalf("error = %v, want ErrUpstreamUnreachable", err)
	}
	if len(secrets.usedKeyIDs) != probeAttempts {
		t.Fatalf("keys tried = %v, want %d", secrets.usedKeyIDs, probeAttempts)
	}
}

func TestModelAutoReportsWhenNoNVIDIAKeyIsAvailable(t *testing.T) {
	service, _, _, modelID := probeService(t, "keyless-chat")

	if err := service.TestModelAuto(context.Background(), modelID); !errors.Is(err, ErrNVIDIAKeyRequired) {
		t.Fatalf("error = %v, want ErrNVIDIAKeyRequired", err)
	}
}

// A mixed selection dispatches on each model's own provider: an OpenCodeFree
// model must never consume an NVIDIA key.
func TestModelAutoDispatchesOnTheModelsOwnProvider(t *testing.T) {
	service, db, secrets, _ := newCatalogTestService(t)
	secrets.availableIDs = []int64{11}
	insertOpenCodeFreeModel(t, db, "opencodefree/model")

	err := service.TestModelAuto(context.Background(), modelIDByPublicID(t, db, "opencodefree/model"))
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("error = %v, want ErrProviderNotConfigured", err)
	}
	if len(secrets.usedKeyIDs) != 0 {
		t.Fatalf("OpenCodeFree probe consumed NVIDIA keys %v", secrets.usedKeyIDs)
	}
}

func insertOpenCodeFreeModel(t *testing.T, db *sql.DB, publicID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, provider, enabled, created_at, updated_at)
		VALUES (?, ?, ?, 'chat', ?, 1, '2026-07-30T04:00:00Z', '2026-07-30T04:00:00Z')
	`, publicID, "vendor/"+publicID, publicID, ProviderOpenCodeFree); err != nil {
		t.Fatalf("insert OpenCodeFree model: %v", err)
	}
}

// probeCompletion records whether the probe told the upstream body that the
// answer completed. Non-stream bodies defer their EOF verdict until a validator
// confirms the payload, so a probe that forgets this charges a request failure
// against the pooled exit that served a perfectly good 200.
type probeCompletion struct{ marked bool }

type probeCompletionBody struct {
	io.ReadCloser
	tracker *probeCompletion
}

func (b probeCompletionBody) MarkComplete() { b.tracker.marked = true }

func TestModelProbeDeclaresCompletionOnSuccess(t *testing.T) {
	service, _, discoverer, modelID := probeService(t, "complete-chat", 11)
	tracker := &probeCompletion{}
	discoverer.chatCompletion = tracker

	if err := service.TestModelAuto(context.Background(), modelID); err != nil {
		t.Fatalf("TestModelAuto: %v", err)
	}
	if !tracker.marked {
		t.Fatal("a validated probe must mark completion, otherwise the exit that served it is charged a failure")
	}
}
