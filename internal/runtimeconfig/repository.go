package runtimeconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
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
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			returnErr = fmt.Errorf("rollback runtime settings transaction: %w", errors.Join(returnErr, rollbackErr))
		}
	}()
	// Emit updated_at as RFC3339 'Z' from the application layer so the row
	// matches the format every other repository writes (admins/nvidia_keys/
	// models all use UTC RFC3339). CURRENT_TIMESTAMP falls back to SQLite's
	// "YYYY-MM-DD HH:MM:SS" default, which is inconsistent to parse back.
	result, err := tx.ExecContext(ctx, `
		UPDATE runtime_settings SET
			queue_capacity = ?, queue_wait_timeout_ms = ?, connect_timeout_ms = ?,
			first_byte_timeout_ms = ?, nonstream_total_timeout_ms = ?, shutdown_grace_ms = ?,
			failover_status_codes = ?, request_log_retention_days = ?,
			max_attempts_per_request = ?, retry_budget_ms = ?, max_streaming_per_key = ?,
			stream_first_token_timeout_ms = ?, stream_idle_timeout_ms = ?,
			latency_routing_enabled = ?, embedding_cache_enabled = ?,
			embedding_cache_max_entries = ?,
			updated_at = ?
		WHERE id = 1`,
		next.QueueCapacity, next.QueueWaitTimeoutMS, next.ConnectTimeoutMS,
		next.FirstByteTimeoutMS, next.NonstreamTotalTimeoutMS, next.ShutdownGraceMS,
		next.FailoverStatusCodes, next.RequestLogRetentionDays,
		next.MaxAttemptsPerRequest, next.RetryBudgetMS, next.MaxStreamingPerKey,
		next.StreamFirstTokenTimeoutMS, next.StreamIdleTimeoutMS,
		boolInt(next.LatencyRoutingEnabled), boolInt(next.EmbeddingCacheEnabled),
		next.EmbeddingCacheMaxEntries,
		formatTimestamp(time.Now()),
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

func formatTimestamp(now time.Time) string {
	return now.UTC().Truncate(time.Second).Format(time.RFC3339)
}

type snapshotQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadSnapshot(ctx context.Context, source snapshotQuerier) (Snapshot, error) {
	var snapshot Snapshot
	var latencyEnabled, cacheEnabled int
	err := source.QueryRowContext(ctx, `
		SELECT queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
			first_byte_timeout_ms, nonstream_total_timeout_ms, shutdown_grace_ms,
			failover_status_codes, request_log_retention_days,
			max_attempts_per_request, retry_budget_ms, max_streaming_per_key,
			stream_first_token_timeout_ms, stream_idle_timeout_ms,
			latency_routing_enabled, embedding_cache_enabled, embedding_cache_max_entries
		FROM runtime_settings WHERE id = 1`).Scan(
		&snapshot.QueueCapacity,
		&snapshot.QueueWaitTimeoutMS,
		&snapshot.ConnectTimeoutMS,
		&snapshot.FirstByteTimeoutMS,
		&snapshot.NonstreamTotalTimeoutMS,
		&snapshot.ShutdownGraceMS,
		&snapshot.FailoverStatusCodes,
		&snapshot.RequestLogRetentionDays,
		&snapshot.MaxAttemptsPerRequest,
		&snapshot.RetryBudgetMS,
		&snapshot.MaxStreamingPerKey,
		&snapshot.StreamFirstTokenTimeoutMS,
		&snapshot.StreamIdleTimeoutMS,
		&latencyEnabled,
		&cacheEnabled,
		&snapshot.EmbeddingCacheMaxEntries,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load runtime settings: %w", err)
	}
	snapshot.LatencyRoutingEnabled = latencyEnabled != 0
	snapshot.EmbeddingCacheEnabled = cacheEnabled != 0
	return snapshot, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
