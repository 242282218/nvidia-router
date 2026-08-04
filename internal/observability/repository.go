package observability

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Record(ctx context.Context, record RequestRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin request record transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertRequestRecord(ctx, tx, record); err != nil {
		return err
	}
	for _, dimension := range recordDimensions(record) {
		if err := upsertDailyStat(ctx, tx, record, dimension); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request record transaction: %w", err)
	}
	return nil
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

	for _, record := range records {
		if err := insertRequestRecord(ctx, tx, record); err != nil {
			return err
		}
		for _, dimension := range recordDimensions(record) {
			if err := upsertDailyStat(ctx, tx, record, dimension); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request batch transaction: %w", err)
	}
	return nil
}

func (r *Repository) DeleteRequestLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM request_logs
		WHERE created_at < ?
	`, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("delete expired request logs: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted request logs: %w", err)
	}
	return count, nil
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

func insertRequestRecord(ctx context.Context, tx *sql.Tx, record RequestRecord) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO request_logs (
			request_id, endpoint, model_id, access_key_id, nvidia_key_id,
			http_status, outcome, error_code, is_stream, queue_ms,
			first_byte_ms, duration_ms, attempt_count, prompt_tokens,
			completion_tokens, upstream_request_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.RequestID, record.Endpoint, nullableString(record.ModelID), record.AccessKeyID, record.NVIDIAKeyID,
		record.HTTPStatus, record.Outcome, record.ErrorCode, boolInt(record.IsStream), record.QueueMS,
		record.FirstByteMS, record.DurationMS, record.AttemptCount, record.PromptTokens,
		record.CompletionTokens, record.UpstreamRequestID, formatTime(record.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert request log: %w", err)
	}
	return nil
}

func upsertDailyStat(ctx context.Context, tx *sql.Tx, record RequestRecord, dimension dimension) error {
	success, failure := outcomeCounts(record.Outcome)
	firstByteMS, firstByteCount := firstByteAggregate(record.FirstByteMS)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO daily_stats (
			day, dimension_type, dimension_id, request_count, success_count,
			failure_count, total_duration_ms, total_queue_ms, total_attempts,
			total_first_byte_ms, first_byte_count, prompt_tokens, completion_tokens
		) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, dimension_type, dimension_id) DO UPDATE SET
			request_count = request_count + 1,
			success_count = success_count + excluded.success_count,
			failure_count = failure_count + excluded.failure_count,
			total_duration_ms = total_duration_ms + excluded.total_duration_ms,
			total_queue_ms = total_queue_ms + excluded.total_queue_ms,
			total_attempts = total_attempts + excluded.total_attempts,
			total_first_byte_ms = total_first_byte_ms + excluded.total_first_byte_ms,
			first_byte_count = first_byte_count + excluded.first_byte_count,
			prompt_tokens = prompt_tokens + excluded.prompt_tokens,
			completion_tokens = completion_tokens + excluded.completion_tokens
	`,
		record.CreatedAt.UTC().Format("2006-01-02"), dimension.typeName, dimension.id,
		success, failure, record.DurationMS, record.QueueMS, record.AttemptCount,
		firstByteMS, firstByteCount,
		valueOrZero(record.PromptTokens), valueOrZero(record.CompletionTokens),
	)
	if err != nil {
		return fmt.Errorf("upsert %s daily stats: %w", dimension.typeName, err)
	}
	return nil
}

func firstByteAggregate(value *int64) (int64, int64) {
	if value == nil {
		return 0, 0
	}
	return *value, 1
}

func outcomeCounts(outcome string) (int, int) {
	if outcome == OutcomeSuccess {
		return 1, 0
	}
	return 0, 1
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
