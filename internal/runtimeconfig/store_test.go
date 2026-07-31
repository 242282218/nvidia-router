package runtimeconfig

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"nvidia-router/internal/database"
)

func TestSnapshotIsSafeDuringConcurrentReadsAndUpdates(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()
	store, err := New(context.Background(), db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	var readers sync.WaitGroup
	for range 16 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 1000 {
				snapshot := store.Snapshot()
				if snapshot.QueueCapacity < 100 || snapshot.QueueCapacity > 119 {
					t.Errorf("queue capacity = %d", snapshot.QueueCapacity)
					return
				}
			}
		}()
	}
	for capacity := 100; capacity < 120; capacity++ {
		next := store.Snapshot()
		next.QueueCapacity = capacity
		if err := store.Store(context.Background(), next); err != nil {
			t.Fatalf("store capacity %d: %v", capacity, err)
		}
	}
	readers.Wait()
}

func TestConcurrentStoresPublishTheLastCommittedSnapshot(t *testing.T) {
	firstCommitted := make(chan struct{})
	releaseFirst := make(chan struct{})
	store := newStore(Snapshot{QueueCapacity: 100}, settingsWriterFunc(func(_ context.Context, next Snapshot) error {
		if next.QueueCapacity == 101 {
			close(firstCommitted)
			<-releaseFirst
		}
		return nil
	}))
	first := store.Snapshot()
	first.QueueCapacity = 101
	second := first
	second.QueueCapacity = 102
	firstDone := make(chan error, 1)
	go func() { firstDone <- store.Store(context.Background(), first) }()
	<-firstCommitted
	secondDone := make(chan error, 1)
	go func() { secondDone <- store.Store(context.Background(), second) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second Store completed before first was published: %v", err)
	default:
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Store: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Store: %v", err)
	}
	if got := store.Snapshot(); got != second {
		t.Fatalf("snapshot = %#v, want %#v", got, second)
	}
}

type settingsWriterFunc func(context.Context, Snapshot) error

func (f settingsWriterFunc) Store(ctx context.Context, snapshot Snapshot) error {
	return f(ctx, snapshot)
}

func TestStoreDoesNotReplaceSnapshotWhenTransactionFails(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store, err := New(context.Background(), db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	before := store.Snapshot()
	next := before
	next.QueueCapacity++
	if _, err := db.Exec("DROP TABLE runtime_settings"); err != nil {
		t.Fatalf("drop runtime settings: %v", err)
	}

	if err := store.Store(context.Background(), next); err == nil {
		t.Fatal("Store succeeded after runtime_settings was removed")
	}
	if got := store.Snapshot(); got != before {
		t.Fatalf("snapshot after failed store = %#v, want %#v", got, before)
	}
}

func TestStoreReplacesSnapshotAfterCommit(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store, err := New(context.Background(), db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	next := store.Snapshot()
	next.QueueCapacity = 101

	if err := store.Store(context.Background(), next); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got := store.Snapshot(); got != next {
		t.Fatalf("snapshot = %#v, want %#v", got, next)
	}
}

func TestStoreDoesNotReplaceSnapshotWhenSettingsRowIsMissing(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store, err := New(context.Background(), db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	before := store.Snapshot()
	next := before
	next.QueueCapacity++
	if _, err := db.Exec("DELETE FROM runtime_settings WHERE id = 1"); err != nil {
		t.Fatalf("delete runtime settings: %v", err)
	}

	if err := store.Store(context.Background(), next); err == nil {
		t.Fatal("Store succeeded without a runtime settings row")
	}
	if got := store.Snapshot(); got != before {
		t.Fatalf("snapshot after missing-row store = %#v, want %#v", got, before)
	}
}
