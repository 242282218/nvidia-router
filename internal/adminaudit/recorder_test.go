package adminaudit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type capturingRepository struct{ entries []Entry }

func (r *capturingRepository) Insert(_ context.Context, entry Entry) (int64, error) {
	r.entries = append(r.entries, entry)
	return int64(len(r.entries)), nil
}

// The repository writes created_at from the entry, so an unset timestamp stamps
// every audit row with the zero time and the trail can no longer be ordered or
// filtered by when the action happened.
func TestRecordStampsCreatedAt(t *testing.T) {
	repository := &capturingRepository{}
	recorder := NewRecorder(repository, slog.New(slog.NewTextHandler(io.Discard, nil)))

	before := time.Now().UTC().Add(-time.Second)
	recorder.Record(context.Background(), "access_keys.create", "access_key", "7", nil)
	after := time.Now().UTC().Add(time.Second)

	if len(repository.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(repository.entries))
	}
	created := repository.entries[0].CreatedAt
	if created.IsZero() {
		t.Fatal("CreatedAt is the zero time; every audit row would be 0001-01-01")
	}
	if created.Before(before) || created.After(after) {
		t.Fatalf("CreatedAt = %s, want between %s and %s", created, before, after)
	}
}
