package observability

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Repository struct {
	db     *sql.DB
	reader *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// WithReader routes monitoring queries to a separate connection pool. Those
// queries scan request_logs and can run for seconds; on the single writer
// connection they blocked request-log flushes and access-key authentication.
func (r *Repository) WithReader(reader *sql.DB) *Repository {
	clone := *r
	clone.reader = reader
	return &clone
}

func (r *Repository) read() *sql.DB {
	if r.reader != nil {
		return r.reader
	}
	return r.db
}

const insertRequestLogQuery = `
	INSERT INTO request_logs (
		request_id, endpoint, model_id, access_key_id, nvidia_key_id,
		http_status, outcome, error_code, is_stream, queue_ms,
		first_byte_ms, first_token_ms, duration_ms, attempt_count,
		prompt_tokens, completion_tokens, upstream_request_id, created_at,
		reasoning_requested, reasoning_wire_fields, reasoning_present,
		reasoning_chars, stream_done, route_mode,
		reasoning_requested_level, reasoning_effective_level
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

const upsertDailyStatQuery = `
	INSERT INTO daily_stats (
		day, dimension_type, dimension_id, request_count, success_count,
		failure_count, canceled_count, total_duration_ms, total_queue_ms, total_attempts,
		total_first_byte_ms, first_byte_count,
		total_first_token_ms, first_token_count,
		prompt_tokens, completion_tokens
	) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(day, dimension_type, dimension_id) DO UPDATE SET
		request_count = request_count + 1,
		success_count = success_count + excluded.success_count,
		failure_count = failure_count + excluded.failure_count,
		canceled_count = canceled_count + excluded.canceled_count,
		total_duration_ms = total_duration_ms + excluded.total_duration_ms,
		total_queue_ms = total_queue_ms + excluded.total_queue_ms,
		total_attempts = total_attempts + excluded.total_attempts,
		total_first_byte_ms = total_first_byte_ms + excluded.total_first_byte_ms,
		first_byte_count = first_byte_count + excluded.first_byte_count,
		total_first_token_ms = total_first_token_ms + excluded.total_first_token_ms,
		first_token_count = first_token_count + excluded.first_token_count,
		prompt_tokens = prompt_tokens + excluded.prompt_tokens,
		completion_tokens = completion_tokens + excluded.completion_tokens
`

type stmtRunner interface {
	ExecContext(ctx context.Context, args ...any) (sql.Result, error)
}

func (r *Repository) Record(ctx context.Context, record RequestRecord) error {
	return r.RecordBatch(ctx, []RequestRecord{record})
}

// RecordBatch persists a slice of request records in a single transaction.
// The whole batch commits atomically; a single failing record rolls the
// entire batch back and returns the underlying error. Callers (the buffered
// flusher) decide how to retry or drop on failure rather than split the batch.
func (r *Repository) RecordBatch(ctx context.Context, records []RequestRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin request batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insertStmt, err := tx.PrepareContext(ctx, insertRequestLogQuery)
	if err != nil {
		return fmt.Errorf("prepare insert request log: %w", err)
	}
	defer insertStmt.Close()

	upsertStmt, err := tx.PrepareContext(ctx, upsertDailyStatQuery)
	if err != nil {
		return fmt.Errorf("prepare upsert daily stats: %w", err)
	}
	defer upsertStmt.Close()

	for _, record := range records {
		if err := execInsertRequestRecord(ctx, insertStmt, record); err != nil {
			return err
		}
		for _, dimension := range recordDimensions(record) {
			if err := execUpsertDailyStat(ctx, upsertStmt, record, dimension); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request batch transaction: %w", err)
	}
	return nil
}

// cleanupBatchSize bounds each DELETE pass. A single unbatched DELETE of a
// large retention window held the shared SQLite writer connection for the whole
// scan, stalling request-log flushes and access-key authentication (audit R7).
const cleanupBatchSize = 5000

type execContextFunc func(ctx context.Context, query string, args ...any) (sql.Result, error)

// deleteBatched deletes rows matching a DELETE statement whose placeholders are
// a cutoff value followed by a LIMIT batch size, looping until a pass removes
// fewer rows than the batch. Each pass is short, so the writer connection is
// never held for a full-table sweep.
func deleteBatched(ctx context.Context, exec execContextFunc, batchSize int, statement string, cutoff any) (int64, error) {
	var deleted int64
	batch := 0
	for {
		batch++
		started := time.Now()
		result, err := exec(ctx, statement, cutoff, batchSize)
		if err != nil {
			return deleted, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += count
		slog.Default().Info("observability cleanup batch completed", "batch", batch, "deleted", count, "total_deleted", deleted, "duration_ms", time.Since(started).Milliseconds())
		if count < int64(batchSize) {
			return deleted, nil
		}
	}
}

func (r *Repository) DeleteRequestLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	// request_logs has a TEXT primary key, so the batched delete keys on the
	// implicit rowid (SQLite always provides one for non-WITHOUT-ROWID tables).
	deleted, err := deleteBatched(ctx, r.db.ExecContext, cleanupBatchSize, `
		DELETE FROM request_logs
		WHERE rowid IN (
			SELECT rowid FROM request_logs
			WHERE created_at < ?
			LIMIT ?
		)`, formatTime(cutoff))
	if err != nil {
		return deleted, fmt.Errorf("delete expired request logs: %w", err)
	}
	return deleted, nil
}

// DeleteDailyStatsBefore prunes aggregate rows older than cutoff. daily_stats
// previously had no cleanup at all: row count grows as day x dimension, so every
// distinct model, NVIDIA key and access key added a row per day forever. The
// retention is deliberately much longer than request_logs, because surviving
// request-log deletion is the whole point of the aggregates.
func (r *Repository) DeleteDailyStatsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	deleted, err := deleteBatched(ctx, r.db.ExecContext, cleanupBatchSize, `
		DELETE FROM daily_stats
		WHERE rowid IN (
			SELECT rowid FROM daily_stats
			WHERE day < ?
			LIMIT ?
		)`, cutoff.UTC().Format("2006-01-02"))
	if err != nil {
		return deleted, fmt.Errorf("delete expired daily stats: %w", err)
	}
	return deleted, nil
}

// MetricsSummary returns label-free global request counters for Prometheus.
// It reads pre-aggregated daily_stats rather than scanning request_logs.
// Canceled (499) is kept as a separate gauge so 30% TTFT-driven cancels do
// not appear as 70% failure in Prometheus alerts.
func (r *Repository) MetricsSummary(ctx context.Context) (MetricsSummary, error) {
	var summary MetricsSummary
	err := r.read().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(request_count), 0),
		       COALESCE(SUM(success_count), 0),
	       COALESCE(SUM(failure_count), 0),
	       COALESCE(SUM(canceled_count), 0)
		FROM daily_stats
		WHERE dimension_type = ? AND dimension_id = ?`,
		DimensionGlobal, GlobalDimensionID,
	).Scan(&summary.Requests, &summary.Successes, &summary.Failures, &summary.Canceled)
	if err != nil && strings.Contains(err.Error(), "canceled_count") {
		// Fallback for DBs that have not yet run the 033 migration.
		err = r.read().QueryRowContext(ctx, `
			SELECT COALESCE(SUM(request_count), 0),
			       COALESCE(SUM(success_count), 0),
		       COALESCE(SUM(failure_count), 0)
			FROM daily_stats
			WHERE dimension_type = ? AND dimension_id = ?`,
			DimensionGlobal, GlobalDimensionID,
		).Scan(&summary.Requests, &summary.Successes, &summary.Failures)
	}
	if err != nil {
		return MetricsSummary{}, fmt.Errorf("load metrics summary: %w", err)
	}
	return summary, nil
}

type dimension struct {
	typeName string
	id       string
}

func recordDimensions(record RequestRecord) []dimension {
	dimensions := []dimension{{typeName: DimensionGlobal, id: GlobalDimensionID}}
	if record.ModelID != "" {
		dimensions = append(dimensions, dimension{typeName: DimensionModel, id: record.ModelID})
	}
	if record.NVIDIAKeyID != nil {
		dimensions = append(dimensions, dimension{typeName: DimensionNVIDIAKey, id: strconv.FormatInt(*record.NVIDIAKeyID, 10)})
	}
	if record.AccessKeyID != nil {
		dimensions = append(dimensions, dimension{typeName: DimensionAccessKey, id: strconv.FormatInt(*record.AccessKeyID, 10)})
	}
	return dimensions
}

func execInsertRequestRecord(ctx context.Context, runner stmtRunner, record RequestRecord) error {
	_, err := runner.ExecContext(ctx,
		record.RequestID, record.Endpoint, nullableString(record.ModelID), record.AccessKeyID, record.NVIDIAKeyID,
		record.HTTPStatus, record.Outcome, record.ErrorCode, boolInt(record.IsStream), record.QueueMS,
		record.FirstByteMS, record.FirstTokenMS, record.DurationMS, record.AttemptCount,
		record.PromptTokens, record.CompletionTokens, record.UpstreamRequestID, formatTime(record.CreatedAt),
		boolInt(record.ReasoningRequested), nullableString(record.ReasoningWireFields), boolInt(record.ReasoningPresent),
		record.ReasoningChars, boolInt(record.StreamDone), nullableString(record.RouteMode),
		nullableString(record.ReasoningRequestedLevel), nullableString(record.ReasoningEffectiveLevel),
	)
	if err != nil {
		return fmt.Errorf("insert request log: %w", err)
	}
	return nil
}

func execUpsertDailyStat(ctx context.Context, runner stmtRunner, record RequestRecord, dimension dimension) error {
	success, failure := outcomeCounts(record.Outcome)
	canceled := 0
	if record.Outcome == OutcomeCanceled {
		canceled = 1
	}
	firstByteMS, firstByteCount := firstByteAggregate(record.FirstByteMS)
	firstTokenMS, firstTokenCount := firstByteAggregate(record.FirstTokenMS)
	_, err := runner.ExecContext(ctx,
		record.CreatedAt.UTC().Format("2006-01-02"), dimension.typeName, dimension.id,
		success, failure, canceled, record.DurationMS, record.QueueMS, record.AttemptCount,
		firstByteMS, firstByteCount, firstTokenMS, firstTokenCount,
		valueOrZero(record.PromptTokens), valueOrZero(record.CompletionTokens),
	)
	if err != nil {
		// Fallback for DBs that have not yet run the 033 migration (canceled_count
		// column missing). Downgrade to the legacy upsert so a rolling deploy does
		// not hard-fail on the first request after the code ships.
		if isMissingCanceledColumn(err) {
			_, fallbackErr := runner.ExecContext(ctx, `
				INSERT INTO daily_stats (
					day, dimension_type, dimension_id, request_count, success_count,
					failure_count, total_duration_ms, total_queue_ms, total_attempts,
					total_first_byte_ms, first_byte_count,
					total_first_token_ms, first_token_count,
					prompt_tokens, completion_tokens
				) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(day, dimension_type, dimension_id) DO UPDATE SET
					request_count = request_count + 1,
					success_count = success_count + excluded.success_count,
					failure_count = failure_count + excluded.failure_count,
					total_duration_ms = total_duration_ms + excluded.total_duration_ms,
					total_queue_ms = total_queue_ms + excluded.total_queue_ms,
					total_attempts = total_attempts + excluded.total_attempts,
					total_first_byte_ms = total_first_byte_ms + excluded.total_first_byte_ms,
					first_byte_count = first_byte_count + excluded.first_byte_count,
					total_first_token_ms = total_first_token_ms + excluded.total_first_token_ms,
					first_token_count = first_token_count + excluded.first_token_count,
					prompt_tokens = prompt_tokens + excluded.prompt_tokens,
					completion_tokens = completion_tokens + excluded.completion_tokens
			`, record.CreatedAt.UTC().Format("2006-01-02"), dimension.typeName, dimension.id,
				success, failure, record.DurationMS, record.QueueMS, record.AttemptCount,
				firstByteMS, firstByteCount, firstTokenMS, firstTokenCount,
				valueOrZero(record.PromptTokens), valueOrZero(record.CompletionTokens),
			)
			if fallbackErr != nil {
				return fmt.Errorf("upsert %s daily stats (fallback): %w", dimension.typeName, fallbackErr)
			}
			return nil
		}
		return fmt.Errorf("upsert %s daily stats: %w", dimension.typeName, err)
	}
	return nil
}

func isMissingCanceledColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "canceled_count") && strings.Contains(msg, "no column")
}

func firstByteAggregate(value *int64) (int64, int64) {
	if value == nil {
		return 0, 0
	}
	return *value, 1
}

func outcomeCounts(outcome string) (int, int) {
	switch outcome {
	case OutcomeSuccess:
		return 1, 0
	case OutcomeCanceled:
		return 0, 0
	default:
		return 0, 1
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// formatTime renders a fixed-width millisecond UTC timestamp. Lexicographic
// order of the result equals chronological order, which is what lets
// request_logs.created_at comparisons and sorts use plain TEXT indexes.
func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
