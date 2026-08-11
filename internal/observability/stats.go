package observability

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const maxRecentErrors = 100

func (r *Repository) ListDailyStats(ctx context.Context, since time.Time) ([]DailyStat, error) {
	rows, err := r.read().QueryContext(ctx, `
		SELECT day, dimension_type, dimension_id,
		       request_count, success_count, failure_count,
		       CAST(total_duration_ms AS REAL) / request_count,
		       CAST(total_queue_ms AS REAL) / request_count,
		       CAST(total_attempts AS REAL) / request_count,
		       CASE WHEN first_byte_count > 0 THEN CAST(total_first_byte_ms AS REAL) / first_byte_count ELSE 0 END,
		       CASE WHEN first_token_count > 0 THEN CAST(total_first_token_ms AS REAL) / first_token_count ELSE 0 END,
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
			&stat.AverageFirstByteMS, &stat.AverageFirstTokenMS,
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

// ListDailyCosts returns one row per (day, model) inside [since, until],
// joining daily_stats' model dimension against the models table's operator-set
// token prices. A model without pricing contributes zero cost but is still
// surfaced (Priced=false) so the UI can prompt the operator to fill prices in.
func (r *Repository) ListDailyCosts(ctx context.Context, since, until time.Time) ([]DailyModelCost, error) {
	rows, err := r.read().QueryContext(ctx, `
		SELECT s.day, s.dimension_id, s.prompt_tokens, s.completion_tokens,
		       COALESCE(m.input_usd_per_mtok, 0),
		       COALESCE(m.output_usd_per_mtok, 0),
		       m.input_usd_per_mtok IS NOT NULL OR m.output_usd_per_mtok IS NOT NULL
		FROM daily_stats s
		LEFT JOIN models m ON m.public_id = s.dimension_id
		WHERE s.dimension_type = ? AND s.day >= ? AND s.day <= ?
		  AND (s.prompt_tokens > 0 OR s.completion_tokens > 0)
		ORDER BY s.day, s.dimension_id
	`, DimensionModel, since.UTC().Format("2006-01-02"), until.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("query daily model costs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	const perMTok = 1_000_000.0
	costs := make([]DailyModelCost, 0)
	for rows.Next() {
		var item DailyModelCost
		var inputPrice, outputPrice float64
		var priced int
		if err := rows.Scan(&item.Day, &item.ModelID, &item.PromptTokens, &item.CompletionTokens,
			&inputPrice, &outputPrice, &priced); err != nil {
			return nil, fmt.Errorf("scan daily model cost: %w", err)
		}
		item.InputCostUSD = float64(item.PromptTokens) / perMTok * inputPrice
		item.OutputCostUSD = float64(item.CompletionTokens) / perMTok * outputPrice
		item.TotalCostUSD = item.InputCostUSD + item.OutputCostUSD
		item.Priced = priced != 0
		costs = append(costs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily model costs: %w", err)
	}
	return costs, nil
}

func (r *Repository) ListRecentErrors(ctx context.Context, limit int) ([]RecentError, error) {	if limit < 1 {
		limit = 1
	}
	if limit > maxRecentErrors {
		limit = maxRecentErrors
	}
	rows, err := r.read().QueryContext(ctx, `
		SELECT request_id, endpoint, model_id, nvidia_key_id, access_key_id,
		       http_status, error_code, upstream_request_id, created_at
		FROM request_logs
		WHERE outcome = ? AND error_code IS NOT NULL
		ORDER BY created_at DESC, request_id DESC
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
