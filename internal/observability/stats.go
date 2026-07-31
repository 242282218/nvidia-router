package observability

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const maxRecentErrors = 100

func (r *Repository) ListDailyStats(ctx context.Context, since time.Time) ([]DailyStat, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT day, dimension_type, dimension_id,
		       request_count, success_count, failure_count,
		       CAST(total_duration_ms AS REAL) / request_count,
		       CAST(total_queue_ms AS REAL) / request_count,
		       CAST(total_attempts AS REAL) / request_count,
		       prompt_tokens, completion_tokens
		FROM daily_stats
		WHERE day >= ?
		ORDER BY day DESC, dimension_type, dimension_id
	`, since.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("query daily stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := make([]DailyStat, 0)
	for rows.Next() {
		var stat DailyStat
		if err := rows.Scan(
			&stat.Day, &stat.DimensionType, &stat.DimensionID,
			&stat.RequestCount, &stat.SuccessCount, &stat.FailureCount,
			&stat.AverageDuration, &stat.AverageQueue, &stat.AverageAttempts,
			&stat.PromptTokens, &stat.CompletionTokens,
		); err != nil {
			return nil, fmt.Errorf("scan daily stats: %w", err)
		}
		stats = append(stats, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily stats: %w", err)
	}
	return stats, nil
}

func (r *Repository) ListRecentErrors(ctx context.Context, limit int) ([]RecentError, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > maxRecentErrors {
		limit = maxRecentErrors
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT request_id, endpoint, model_id, nvidia_key_id, access_key_id,
		       http_status, error_code, upstream_request_id, created_at
		FROM request_logs
		WHERE outcome = ? AND error_code IS NOT NULL
		ORDER BY julianday(created_at) DESC, request_id DESC
		LIMIT ?
	`, OutcomeFailure, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent errors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	errorsList := make([]RecentError, 0)
	for rows.Next() {
		var item RecentError
		var modelID, upstreamRequestID sql.NullString
		var nvidiaKeyID, accessKeyID sql.NullInt64
		if err := rows.Scan(
			&item.RequestID, &item.Endpoint, &modelID, &nvidiaKeyID, &accessKeyID,
			&item.HTTPStatus, &item.ErrorCode, &upstreamRequestID, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent error: %w", err)
		}
		item.ModelID = nullableStringPointer(modelID)
		item.NVIDIAKeyID = nullableInt64Pointer(nvidiaKeyID)
		item.AccessKeyID = nullableInt64Pointer(accessKeyID)
		item.UpstreamRequestID = nullableStringPointer(upstreamRequestID)
		errorsList = append(errorsList, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent errors: %w", err)
	}
	return errorsList, nil
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func nullableInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
