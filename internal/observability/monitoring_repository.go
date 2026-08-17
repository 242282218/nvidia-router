package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type monitoringAggregate struct {
	requestCount, successCount, failureCount int64
	totalDurationMS, totalFirstByteMS        int64
	firstByteCount, totalQueueMS             int64
	totalFirstTokenMS, firstTokenCount       int64
	totalAttempts, promptTokens              int64
	completionTokens                         int64
}

type monitoringBucketSpec struct {
	count int
	step  time.Duration
}

func (r *Repository) MonitoringSummary(ctx context.Context, query MonitoringQuery) (MonitoringSnapshot, error) {
	spec, err := monitoringBucketSpecFor(query)
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	var aggregate monitoringAggregate
	var buckets map[string]monitoringAggregate
	if useDailyMonitoringStats(query) {
		dimensionType, dimensionID := monitoringDimension(query.Filter)
		aggregate, err = r.queryDailyAggregate(ctx, query, dimensionType, dimensionID)
		if err == nil {
			buckets, err = r.queryDailySeries(ctx, query, dimensionType, dimensionID)
		}
	} else {
		aggregate, err = r.queryRequestAggregate(ctx, query)
		if err == nil {
			buckets, err = r.queryRequestSeries(ctx, query)
		}
	}
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	summary := aggregate.toSummary()
	p50, p95, err := r.queryFirstTokenPercentiles(ctx, query)
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	summary.FirstTokenP50MS = p50
	summary.FirstTokenP95MS = p95
	return MonitoringSnapshot{
		Range: query.Range, From: query.From.UTC(), To: query.To.UTC(),
		Summary: summary, Series: buildMonitoringSeries(query, spec, buckets),
	}, nil
}

// queryFirstTokenPercentiles computes TTFT p50/p95 from the actual
// first_token_ms samples in request_logs within the query window and filter.
// daily_stats only keeps sums and counts, so a true quantile must read the
// per-request column. Instead of pulling every sample into memory and sorting
// it in Go (which, on a 30-day window with tens of thousands of streamed
// requests, loaded the whole column set per summary refresh), the rank values
// are located with COUNT + LIMIT/OFFSET so SQLite discards each ordering pass
// after the single row of interest.
func (r *Repository) queryFirstTokenPercentiles(ctx context.Context, query MonitoringQuery) (*int64, *int64, error) {
	where, args := requestLogWhere(query)
	var count int64
	if err := r.read().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM request_logs
		WHERE `+where+` AND first_token_ms IS NOT NULL
	`, args...).Scan(&count); err != nil {
		return nil, nil, fmt.Errorf("count first token samples: %w", err)
	}
	if count == 0 {
		return nil, nil, nil
	}
	sampleAt := func(q float64) (*int64, error) {
		rank := nearestRankIndex(count, q)
		var value int64
		if err := r.read().QueryRowContext(ctx, `
			SELECT first_token_ms FROM request_logs
			WHERE `+where+` AND first_token_ms IS NOT NULL
			ORDER BY first_token_ms
			LIMIT 1 OFFSET ?
		`, append(args, rank)...).Scan(&value); err != nil {
			return nil, fmt.Errorf("query first token sample at rank %d: %w", rank, err)
		}
		return &value, nil
	}
	p50, err := sampleAt(0.50)
	if err != nil {
		return nil, nil, err
	}
	p95, err := sampleAt(0.95)
	if err != nil {
		return nil, nil, err
	}
	return p50, p95, nil
}

// nearestRankIndex returns the 0-based index of the nearest-rank quantile for
// q in [0,1] over count samples, clamped to the first/last index so a single
// sample is both its p50 and p95.
func nearestRankIndex(count int64, q float64) int {
	rank := int(math.Ceil(q*float64(count))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= int(count) {
		rank = int(count) - 1
	}
	return rank
}

func (r *Repository) ListRequestLogs(ctx context.Context, query RequestLogsQuery) (RequestLogsPage, error) {
	page, pageSize, err := normalizeRequestLogsPage(query.Page, query.PageSize)
	if err != nil {
		return RequestLogsPage{}, err
	}
	where, args := requestLogWhere(query.MonitoringQuery)
	var total int64
	if err := r.read().QueryRowContext(ctx, "SELECT COUNT(*) FROM request_logs WHERE "+where, args...).Scan(&total); err != nil {
		return RequestLogsPage{}, fmt.Errorf("count monitoring request logs: %w", err)
	}
	offset := int64(page-1) * int64(pageSize)
	rows, err := r.read().QueryContext(ctx, `
		SELECT request_id, endpoint, model_id, access_key_id, nvidia_key_id,
		       http_status, outcome, error_code, is_stream, queue_ms,
		       first_byte_ms, first_token_ms, duration_ms, attempt_count,
		       prompt_tokens, completion_tokens, upstream_request_id, created_at,
		       reasoning_requested, reasoning_wire_fields, reasoning_present,
		       reasoning_chars, stream_done, route_mode
		FROM request_logs
		WHERE `+where+`
		ORDER BY created_at DESC, request_id DESC
		LIMIT ? OFFSET ?
	`, append(args, pageSize, offset)...)
	if err != nil {
		return RequestLogsPage{}, fmt.Errorf("query monitoring request logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]RequestLog, 0, pageSize)
	for rows.Next() {
		item, err := scanRequestLog(rows)
		if err != nil {
			return RequestLogsPage{}, fmt.Errorf("scan monitoring request log: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return RequestLogsPage{}, fmt.Errorf("iterate monitoring request logs: %w", err)
	}
	return RequestLogsPage{
		Items: items, Page: page, PageSize: pageSize, Total: total,
		HasMore: offset+int64(len(items)) < total,
	}, nil
}

func (r *Repository) queryRequestAggregate(ctx context.Context, query MonitoringQuery) (monitoringAggregate, error) {
	where, args := requestLogWhere(query)
	row := r.read().QueryRowContext(ctx, `SELECT `+monitoringAggregateColumns+` FROM request_logs WHERE `+where, args...)
	aggregate, err := scanMonitoringAggregate(row, nil)
	if err != nil {
		return monitoringAggregate{}, fmt.Errorf("query monitoring request aggregate: %w", err)
	}
	return aggregate, nil
}

func (r *Repository) queryRequestSeries(ctx context.Context, query MonitoringQuery) (map[string]monitoringAggregate, error) {
	where, args := requestLogWhere(query)
	bucket := requestBucketExpression(query.Range)
	rows, err := r.read().QueryContext(ctx, `
		SELECT `+bucket+`, `+monitoringAggregateColumns+`
		FROM request_logs WHERE `+where+`
		GROUP BY `+bucket+`
		ORDER BY `+bucket, args...)
	if err != nil {
		return nil, fmt.Errorf("query monitoring request series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanMonitoringSeries(rows)
}

func (r *Repository) queryDailyAggregate(ctx context.Context, query MonitoringQuery, dimensionType, dimensionID string) (monitoringAggregate, error) {
	row := r.read().QueryRowContext(ctx, `
		SELECT `+dailyAggregateColumns+`
		FROM daily_stats
		WHERE day >= ? AND day <= ? AND dimension_type = ? AND dimension_id = ?
	`, query.From.UTC().Format("2006-01-02"), query.To.UTC().Format("2006-01-02"), dimensionType, dimensionID)
	aggregate, err := scanMonitoringAggregate(row, nil)
	if err != nil {
		return monitoringAggregate{}, fmt.Errorf("query daily monitoring aggregate: %w", err)
	}
	return aggregate, nil
}

func (r *Repository) queryDailySeries(ctx context.Context, query MonitoringQuery, dimensionType, dimensionID string) (map[string]monitoringAggregate, error) {
	rows, err := r.read().QueryContext(ctx, `
		SELECT day, `+dailyAggregateColumns+`
		FROM daily_stats
		WHERE day >= ? AND day <= ? AND dimension_type = ? AND dimension_id = ?
		GROUP BY day
		ORDER BY day
	`, query.From.UTC().Format("2006-01-02"), query.To.UTC().Format("2006-01-02"), dimensionType, dimensionID)
	if err != nil {
		return nil, fmt.Errorf("query daily monitoring series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanMonitoringSeries(rows)
}

const monitoringAggregateColumns = `
	COUNT(*),
	COALESCE(SUM(CASE WHEN outcome = 'success' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN outcome != 'success' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(duration_ms), 0),
	COALESCE(SUM(first_byte_ms), 0),
	COALESCE(SUM(CASE WHEN first_byte_ms IS NOT NULL THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(first_token_ms), 0),
	COALESCE(SUM(CASE WHEN first_token_ms IS NOT NULL THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(queue_ms), 0),
	COALESCE(SUM(attempt_count), 0),
	COALESCE(SUM(prompt_tokens), 0),
	COALESCE(SUM(completion_tokens), 0)`

const dailyAggregateColumns = `
	COALESCE(SUM(request_count), 0),
	COALESCE(SUM(success_count), 0),
	COALESCE(SUM(failure_count), 0),
	COALESCE(SUM(total_duration_ms), 0),
	COALESCE(SUM(total_first_byte_ms), 0),
	COALESCE(SUM(first_byte_count), 0),
	COALESCE(SUM(total_first_token_ms), 0),
	COALESCE(SUM(first_token_count), 0),
	COALESCE(SUM(total_queue_ms), 0),
	COALESCE(SUM(total_attempts), 0),
	COALESCE(SUM(prompt_tokens), 0),
	COALESCE(SUM(completion_tokens), 0)`

func (a monitoringAggregate) toSummary() MonitoringSummary {
	return MonitoringSummary{
		RequestCount: a.requestCount, SuccessCount: a.successCount, FailureCount: a.failureCount,
		SuccessRate:         successRate(a.successCount, a.requestCount),
		AverageDurationMS:   average(a.totalDurationMS, a.requestCount),
		AverageFirstByteMS:  average(a.totalFirstByteMS, a.firstByteCount),
		AverageFirstTokenMS: average(a.totalFirstTokenMS, a.firstTokenCount),
		AverageQueueMS:      average(a.totalQueueMS, a.requestCount),
		TotalAttempts:       a.totalAttempts, PromptTokens: a.promptTokens, CompletionTokens: a.completionTokens,
	}
}

func (a monitoringAggregate) toSeriesPoint(bucket string) MonitoringSeriesPoint {
	summary := a.toSummary()
	return MonitoringSeriesPoint{
		Bucket: bucket, RequestCount: summary.RequestCount, SuccessCount: summary.SuccessCount,
		FailureCount: summary.FailureCount, AverageDurationMS: summary.AverageDurationMS,
		AverageFirstByteMS: summary.AverageFirstByteMS, AverageFirstTokenMS: summary.AverageFirstTokenMS,
		AverageQueueMS: summary.AverageQueueMS,
		TotalAttempts:  summary.TotalAttempts, PromptTokens: summary.PromptTokens,
		CompletionTokens: summary.CompletionTokens,
	}
}

func buildMonitoringSeries(query MonitoringQuery, spec monitoringBucketSpec, values map[string]monitoringAggregate) []MonitoringSeriesPoint {
	series := make([]MonitoringSeriesPoint, 0, spec.count)
	for index := 0; index < spec.count; index++ {
		bucketTime := query.From.Add(time.Duration(index) * spec.step)
		bucket := formatMonitoringBucket(query.Range, bucketTime)
		series = append(series, values[bucket].toSeriesPoint(bucket))
	}
	return series
}

func scanMonitoringSeries(rows *sql.Rows) (map[string]monitoringAggregate, error) {
	values := make(map[string]monitoringAggregate)
	for rows.Next() {
		var bucket string
		aggregate, err := scanMonitoringAggregate(rows, &bucket)
		if err != nil {
			return nil, err
		}
		values[bucket] = aggregate
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitoring series: %w", err)
	}
	return values, nil
}

type monitoringScanner interface {
	Scan(...any) error
}

func scanMonitoringAggregate(scanner monitoringScanner, bucket *string) (monitoringAggregate, error) {
	var aggregate monitoringAggregate
	arguments := make([]any, 0, 13)
	if bucket != nil {
		arguments = append(arguments, bucket)
	}
	arguments = append(arguments,
		&aggregate.requestCount, &aggregate.successCount, &aggregate.failureCount,
		&aggregate.totalDurationMS, &aggregate.totalFirstByteMS, &aggregate.firstByteCount,
		&aggregate.totalFirstTokenMS, &aggregate.firstTokenCount,
		&aggregate.totalQueueMS, &aggregate.totalAttempts, &aggregate.promptTokens,
		&aggregate.completionTokens,
	)
	if err := scanner.Scan(arguments...); err != nil {
		return monitoringAggregate{}, err
	}
	return aggregate, nil
}

func requestBucketExpression(rangeName string) string {
	if rangeName == MonitoringRange24Hours {
		return "substr(created_at, 1, 13) || ':00:00Z'"
	}
	return "substr(created_at, 1, 10)"
}

func formatMonitoringBucket(rangeName string, value time.Time) string {
	if rangeName == MonitoringRange24Hours {
		return value.UTC().Format("2006-01-02T15:00:00Z")
	}
	return value.UTC().Format("2006-01-02")
}

func monitoringBucketSpecFor(query MonitoringQuery) (monitoringBucketSpec, error) {
	switch query.Range {
	case MonitoringRange24Hours:
		return monitoringBucketSpec{count: 24, step: time.Hour}, nil
	case MonitoringRange7Days:
		return monitoringBucketSpec{count: 7, step: 24 * time.Hour}, nil
	case MonitoringRange30Days:
		return monitoringBucketSpec{count: 30, step: 24 * time.Hour}, nil
	default:
		return monitoringBucketSpec{}, errors.New("monitoring range is invalid")
	}
}

func useDailyMonitoringStats(query MonitoringQuery) bool {
	if query.Range == MonitoringRange24Hours {
		return false
	}
	filter := query.Filter
	if filter.Endpoint != "" || filter.Outcome != "" || filter.Search != "" || filter.Status != nil {
		return false
	}
	dimensions := 0
	if filter.ModelID != "" {
		dimensions++
	}
	if filter.AccessKeyID != nil {
		dimensions++
	}
	if filter.NVIDIAKeyID != nil {
		dimensions++
	}
	return dimensions <= 1
}

func monitoringDimension(filter MonitoringFilter) (string, string) {
	switch {
	case filter.ModelID != "":
		return DimensionModel, filter.ModelID
	case filter.AccessKeyID != nil:
		return DimensionAccessKey, strconv.FormatInt(*filter.AccessKeyID, 10)
	case filter.NVIDIAKeyID != nil:
		return DimensionNVIDIAKey, strconv.FormatInt(*filter.NVIDIAKeyID, 10)
	default:
		return DimensionGlobal, GlobalDimensionID
	}
}

func requestLogWhere(query MonitoringQuery) (string, []any) {
	conditions := []string{"created_at >= ?", "created_at < ?"}
	args := []any{formatTime(query.From), formatTime(query.To)}
	filter := query.Filter
	if filter.ModelID != "" {
		conditions = append(conditions, "model_id = ?")
		args = append(args, filter.ModelID)
	}
	if filter.Endpoint != "" {
		conditions = append(conditions, "endpoint = ?")
		args = append(args, filter.Endpoint)
	}
	if filter.Outcome != "" {
		conditions = append(conditions, "outcome = ?")
		args = append(args, filter.Outcome)
	}
	if filter.Status != nil {
		conditions = append(conditions, "http_status = ?")
		args = append(args, *filter.Status)
	}
	if filter.AccessKeyID != nil {
		conditions = append(conditions, "access_key_id = ?")
		args = append(args, *filter.AccessKeyID)
	}
	if filter.NVIDIAKeyID != nil {
		conditions = append(conditions, "nvidia_key_id = ?")
		args = append(args, *filter.NVIDIAKeyID)
	}
	if filter.Search != "" {
		search := "%" + escapeMonitoringSearch(filter.Search) + "%"
		conditions = append(conditions, `(request_id LIKE ? ESCAPE '\' OR endpoint LIKE ? ESCAPE '\' OR model_id LIKE ? ESCAPE '\' OR error_code LIKE ? ESCAPE '\' OR upstream_request_id LIKE ? ESCAPE '\')`)
		for range 5 {
			args = append(args, search)
		}
	}
	return strings.Join(conditions, " AND "), args
}

func escapeMonitoringSearch(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func normalizeRequestLogsPage(page, pageSize int) (int, int, error) {
	if page < 1 || page > 100000 {
		return 0, 0, errors.New("monitoring page is invalid")
	}
	if pageSize < 1 || pageSize > MaxMonitoringPageSize {
		return 0, 0, errors.New("monitoring page size is invalid")
	}
	return page, pageSize, nil
}

func scanRequestLog(rows *sql.Rows) (RequestLog, error) {
	var item RequestLog
	var modelID, errorCode, upstreamRequestID, reasoningWireFields, routeMode sql.NullString
	var accessKeyID, nvidiaKeyID, firstByteMS, firstTokenMS, promptTokens, completionTokens, reasoningChars sql.NullInt64
	var isStream, reasoningRequested, reasoningPresent, streamDone int
	if err := rows.Scan(
		&item.RequestID, &item.Endpoint, &modelID, &accessKeyID, &nvidiaKeyID,
		&item.HTTPStatus, &item.Outcome, &errorCode, &isStream, &item.QueueMS,
		&firstByteMS, &firstTokenMS, &item.DurationMS, &item.AttemptCount, &promptTokens,
		&completionTokens, &upstreamRequestID, &item.CreatedAt,
		&reasoningRequested, &reasoningWireFields, &reasoningPresent, &reasoningChars,
		&streamDone, &routeMode,
	); err != nil {
		return RequestLog{}, err
	}
	item.ModelID = nullableStringPointer(modelID)
	item.AccessKeyID = nullableInt64Pointer(accessKeyID)
	item.NVIDIAKeyID = nullableInt64Pointer(nvidiaKeyID)
	item.ErrorCode = nullableStringPointer(errorCode)
	item.IsStream = isStream != 0
	item.FirstByteMS = nullableInt64Pointer(firstByteMS)
	item.FirstTokenMS = nullableInt64Pointer(firstTokenMS)
	item.PromptTokens = nullableInt64Pointer(promptTokens)
	item.CompletionTokens = nullableInt64Pointer(completionTokens)
	item.UpstreamRequestID = nullableStringPointer(upstreamRequestID)
	item.ReasoningRequested = reasoningRequested != 0
	item.ReasoningWireFields = nullableStringPointer(reasoningWireFields)
	item.ReasoningPresent = reasoningPresent != 0
	item.ReasoningChars = nullableInt64Pointer(reasoningChars)
	item.StreamDone = streamDone != 0
	item.RouteMode = nullableStringPointer(routeMode)
	return item, nil
}

func average(total, count int64) float64 {
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

func successRate(success, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(success) / float64(total) * 100
}
