package adminaudit

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/database"
)

func openTestDB(t *testing.T) *Repository {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(db)
}

func TestRepositoryInsertAndListRoundTrip(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	session := "sess-abc"
	entry := Entry{
		Action: "nvidia_keys.import", TargetType: "nvidia-keys", TargetID: "", Detail: `{"imported":1}`,
		SessionID: &session, ClientIP: "10.0.0.1", CreatedAt: time.Now().UTC(),
	}
	id, err := repo.Insert(ctx, entry)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Insert returned id %d", id)
	}

	page, err := repo.List(ctx, ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("List = total:%d items:%d, want 1/1", page.Total, len(page.Items))
	}
	got := page.Items[0]
	if got.Action != "nvidia_keys.import" || got.ClientIP != "10.0.0.1" {
		t.Fatalf("entry round trip mismatch: %+v", got)
	}
	if got.SessionID == nil || *got.SessionID != "sess-abc" {
		t.Fatalf("session id not preserved: %+v", got.SessionID)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at not parsed")
	}
}

func TestRepositoryListPaginationAndFilter(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	for index := 1; index <= 5; index++ {
		action := "access_keys.create"
		if index%2 == 0 {
			action = "settings.update"
		}
		if _, err := repo.Insert(ctx, Entry{Action: action, ClientIP: "127.0.0.1", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// Newest-first ordering: the 5th inserted (odd index -> access_keys.create)
	// has the highest id and should come first.
	page, err := repo.List(ctx, ListQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 2 || !page.HasMore || page.Next == nil || *page.Next != 2 {
		t.Fatalf("first page = items:%d hasMore:%v next:%v, want 2/true/2", len(page.Items), page.HasMore, page.Next)
	}
	if page.Items[0].Action != "access_keys.create" {
		t.Fatalf("first item action = %s, want newest access_keys.create", page.Items[0].Action)
	}

	filtered, err := repo.List(ctx, ListQuery{Limit: 10, Action: "access_keys.create"})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if filtered.Total != 3 {
		t.Fatalf("filtered total = %d, want 3", filtered.Total)
	}
}

func TestRecorderInsertsWithoutPrincipal(t *testing.T) {
	repo := openTestDB(t)
	recorder := NewRecorder(repo, slog.Default())
	ctx := context.Background()
	recorder.Record(ctx, "auth.login", "auth", "", map[string]any{"ok": false})

	page, err := repo.List(ctx, ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("want 1 entry, got %d", page.Total)
	}
	entry := page.Items[0]
	if entry.SessionID != nil {
		t.Fatalf("expected nil session for unauthenticated, got %v", *entry.SessionID)
	}
}

func TestMarshalDetailEncodesMap(t *testing.T) {
	if encoded := MarshalDetail(map[string]any{"status": 200, "imported": true}); encoded != `{"imported":true,"status":200}` && encoded != `{"status":200,"imported":true}` {
		t.Fatalf("MarshalDetail order-insensitive mismatch: %s", encoded)
	}
	if empty := MarshalDetail(nil); empty != "" {
		t.Fatalf("MarshalDetail(nil) = %q, want empty", empty)
	}
}
