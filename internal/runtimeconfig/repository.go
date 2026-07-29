package runtimeconfig

import (
	"context"
	"database/sql"
	"fmt"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Load(ctx context.Context) (Snapshot, error) {
	return loadSnapshot(ctx, r.db)
}

func (r *Repository) Store(ctx context.Context, next Snapshot) (returnErr error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime settings transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			returnErr = fmt.Errorf("rollback runtime settings transaction: %w", rollbackErr)
		}
	}()
	result, err := tx.ExecContext(ctx, `
		UPDATE runtime_settings SET
			queue_capacity = ?, queue_wait_timeout_ms = ?, connect_timeout_ms = ?,
			first_byte_timeout_ms = ?, nonstream_total_timeout_ms = ?, shutdown_grace_ms = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		next.QueueCapacity, next.QueueWaitTimeoutMS, next.ConnectTimeoutMS,
		next.FirstByteTimeoutMS, next.NonstreamTotalTimeoutMS, next.ShutdownGraceMS,
	)
	if err != nil {
		return fmt.Errorf("update runtime settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated runtime settings: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("update runtime settings: expected one row, updated %d", updated)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime settings transaction: %w", err)
	}
	committed = true
	return nil
}

type snapshotQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadSnapshot(ctx context.Context, source snapshotQuerier) (Snapshot, error) {
	var snapshot Snapshot
	err := source.QueryRowContext(ctx, `
		SELECT queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
			first_byte_timeout_ms, nonstream_total_timeout_ms, shutdown_grace_ms
		FROM runtime_settings WHERE id = 1`).Scan(
		&snapshot.QueueCapacity,
		&snapshot.QueueWaitTimeoutMS,
		&snapshot.ConnectTimeoutMS,
		&snapshot.FirstByteTimeoutMS,
		&snapshot.NonstreamTotalTimeoutMS,
		&snapshot.ShutdownGraceMS,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load runtime settings: %w", err)
	}
	return snapshot, nil
}
