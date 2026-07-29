package runtimeconfig

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
)

type Store struct {
	repository settingsWriter
	snapshot   atomic.Value
	storeMu    sync.Mutex
}

type settingsWriter interface {
	Store(context.Context, Snapshot) error
}

func New(ctx context.Context, db *sql.DB) (*Store, error) {
	repository := NewRepository(db)
	snapshot, err := repository.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load initial runtime settings: %w", err)
	}
	return newStore(snapshot, repository), nil
}

func newStore(snapshot Snapshot, repository settingsWriter) *Store {
	store := &Store{repository: repository}
	store.snapshot.Store(snapshot)
	return store
}

func (s *Store) Snapshot() Snapshot {
	return s.snapshot.Load().(Snapshot)
}

func (s *Store) Store(ctx context.Context, next Snapshot) error {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if err := s.repository.Store(ctx, next); err != nil {
		return fmt.Errorf("store runtime settings: %w", err)
	}
	s.snapshot.Store(next)
	return nil
}
