package runtimeconfig

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
)

type Store struct {
	repository *Repository
	snapshot   atomic.Value
}

func New(ctx context.Context, db *sql.DB) (*Store, error) {
	repository := NewRepository(db)
	snapshot, err := repository.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load initial runtime settings: %w", err)
	}
	store := &Store{repository: repository}
	store.snapshot.Store(snapshot)
	return store, nil
}

func (s *Store) Snapshot() Snapshot {
	return s.snapshot.Load().(Snapshot)
}

func (s *Store) Store(ctx context.Context, next Snapshot) error {
	if err := s.repository.Store(ctx, next); err != nil {
		return fmt.Errorf("store runtime settings: %w", err)
	}
	s.snapshot.Store(next)
	return nil
}
