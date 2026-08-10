package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestNewMonitoringQueryBuildsSupportedWindows(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 34, 56, 0, time.FixedZone("UTC+8", 8*60*60))
	query, err := NewMonitoringQuery(now, MonitoringRange24Hours, MonitoringFilter{})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	if query.Range != MonitoringRange24Hours {
		t.Fatalf("range = %q, want %q", query.Range, MonitoringRange24Hours)
	}
	if !query.From.Equal(time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("from = %s, want 2026-08-02T05:00:00Z", query.From)
	}
	if !query.To.Equal(now.UTC()) {
		t.Fatalf("to = %s, want %s", query.To, now.UTC())
	}
}

func TestRepositoryMonitoringSummaryFillsEmptyBuckets(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 3, 12, 34, 56, 0, time.UTC)
	for _, record := range []RequestRecord{
		{RequestID: "hour-success", Endpoint: "/v1/chat/completions", ModelID: "chat-model", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 100, QueueMS: 10, AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 11, 5, 0, 0, time.UTC)},
		{RequestID: "hour-failure", Endpoint: "/v1/chat/completions", ModelID: "chat-model", HTTPStatus: http.StatusBadGateway, Outcome: OutcomeFailure, DurationMS: 300, QueueMS: 30, AttemptCount: 2, CreatedAt: time.Date(2026, 8, 3, 9, 20, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	query, err := NewMonitoringQuery(now, MonitoringRange24Hours, MonitoringFilter{})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	snapshot, err := repository.MonitoringSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("MonitoringSummary: %v", err)
	}
	if len(snapshot.Series) != 24 {
		t.Fatalf("series length = %d, want 24", len(snapshot.Series))
	}
	if snapshot.Summary.RequestCount != 2 || snapshot.Summary.SuccessCount != 1 || snapshot.Summary.FailureCount != 1 {
		t.Fatalf("summary counts = %#v", snapshot.Summary)
	}
	if snapshot.Summary.AverageDurationMS != 200 || snapshot.Summary.AverageQueueMS != 20 {
		t.Fatalf("summary averages = %#v", snapshot.Summary)
	}
	if got := seriesPoint(snapshot.Series, "2026-08-03T10:00:00Z").RequestCount; got != 0 {
		t.Fatalf("empty bucket request count = %d, want 0", got)
	}
	if got := seriesPoint(snapshot.Series, "2026-08-03T11:00:00Z").RequestCount; got != 1 {
		t.Fatalf("11:00 bucket request count = %d, want 1", got)
	}
}

func TestRepositoryMonitoringSummaryUsesDailyDimensionsForThirtyDays(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for _, record := range []RequestRecord{
		{RequestID: "model-old", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 100, FirstByteMS: int64Pointer(40), QueueMS: 10, AttemptCount: 1, CreatedAt: time.Date(2026, 7, 10, 2, 0, 0, 0, time.UTC)},
		{RequestID: "model-new", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 300, FirstByteMS: int64Pointer(160), QueueMS: 30, AttemptCount: 3, CreatedAt: time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC)},
		{RequestID: "other-model", Endpoint: "/v1/chat/completions", ModelID: "model-b", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 900, QueueMS: 90, AttemptCount: 9, CreatedAt: time.Date(2026, 8, 2, 3, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	query, err := NewMonitoringQuery(now, MonitoringRange30Days, MonitoringFilter{ModelID: "model-a"})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	snapshot, err := repository.MonitoringSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("MonitoringSummary: %v", err)
	}
	if snapshot.Summary.RequestCount != 2 || snapshot.Summary.TotalAttempts != 4 {
		t.Fatalf("model summary counts = %#v", snapshot.Summary)
	}
	if snapshot.Summary.AverageDurationMS != 200 || snapshot.Summary.AverageFirstByteMS != 100 || snapshot.Summary.AverageQueueMS != 20 {
		t.Fatalf("model summary averages = %#v", snapshot.Summary)
	}
}

func TestRepositoryListRequestLogsFiltersAndPaginates(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, record := range []RequestRecord{
		{RequestID: "page-old", Endpoint: "/v1/models", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 10, AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)},
		{RequestID: "page-new", Endpoint: "/v1/models", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 20, AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)},
		{RequestID: "page-error", Endpoint: "/v1/chat/completions", ModelID: "model-b", HTTPStatus: http.StatusBadGateway, Outcome: OutcomeFailure, DurationMS: 30, AttemptCount: 2, CreatedAt: time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	query, err := NewMonitoringQuery(time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC), MonitoringRange24Hours, MonitoringFilter{Search: "page", Outcome: OutcomeSuccess})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	logsQuery := RequestLogsQuery{MonitoringQuery: query, Page: 1, PageSize: 1}
	page, err := repository.ListRequestLogs(context.Background(), logsQuery)
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}
	if page.Total != 2 || !page.HasMore || len(page.Items) != 1 || page.Items[0].RequestID != "page-new" {
		t.Fatalf("page 1 = %#v", page)
	}
	logsQuery.Page = 2
	page, err = repository.ListRequestLogs(context.Background(), logsQuery)
	if err != nil {
		t.Fatalf("ListRequestLogs page 2: %v", err)
	}
	if page.HasMore || len(page.Items) != 1 || page.Items[0].RequestID != "page-old" {
		t.Fatalf("page 2 = %#v", page)
	}
}

func TestRepositoryMonitoringEscapesSearchWildcards(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, requestID := range []string{"literal%id", "literalXid", "literal_id", "literalXid2"} {
		if err := repository.Record(context.Background(), RequestRecord{
			RequestID: requestID, Endpoint: "/v1/models", HTTPStatus: http.StatusOK,
			Outcome: OutcomeSuccess, DurationMS: 1, AttemptCount: 1,
			CreatedAt: time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("Record %s: %v", requestID, err)
		}
	}
	query, err := NewMonitoringQuery(time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC), MonitoringRange24Hours, MonitoringFilter{Search: "%"})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	logsQuery := RequestLogsQuery{MonitoringQuery: query, Page: 1, PageSize: 100}
	page, err := repository.ListRequestLogs(context.Background(), logsQuery)
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].RequestID != "literal%id" {
		t.Fatalf("literal percent search = %#v", page)
	}
}

func TestRepositoryMonitoringSummaryAveragesFirstToken(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for _, record := range []RequestRecord{
		{RequestID: "tok-old", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true, DurationMS: 200, FirstTokenMS: int64Pointer(50), AttemptCount: 1, CreatedAt: time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC)},
		{RequestID: "tok-new", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true, DurationMS: 200, FirstTokenMS: int64Pointer(150), AttemptCount: 1, CreatedAt: time.Date(2026, 8, 2, 3, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	query, err := NewMonitoringQuery(now, MonitoringRange30Days, MonitoringFilter{ModelID: "model-a"})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	snapshot, err := repository.MonitoringSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("MonitoringSummary: %v", err)
	}
	if snapshot.Summary.AverageFirstTokenMS != 100 {
		t.Fatalf("average first token = %f, want 100", snapshot.Summary.AverageFirstTokenMS)
	}
}

func TestRepositoryListRequestLogsIncludesFirstToken(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	firstToken := int64(90)
	if err := repository.Record(context.Background(), RequestRecord{
		RequestID: "log-token", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK,
		Outcome: OutcomeSuccess, IsStream: true, DurationMS: 100, FirstTokenMS: &firstToken,
		AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	query := RequestLogsQuery{MonitoringQuery: MonitoringQuery{
		Range: MonitoringRange24Hours,
		From:  time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
		To:    time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	}, Page: 1, PageSize: 10}
	page, err := repository.ListRequestLogs(context.Background(), query)
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].FirstTokenMS == nil || *page.Items[0].FirstTokenMS != firstToken {
		t.Fatalf("request log first_token_ms = %#v, want %d", page.Items, firstToken)
	}
}

func TestRepositoryListDailyStatsAveragesFirstToken(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, record := range []RequestRecord{
		{RequestID: "daily-tok-1", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true, DurationMS: 100, FirstTokenMS: int64Pointer(40), AttemptCount: 1, CreatedAt: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)},
		{RequestID: "daily-tok-2", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true, DurationMS: 100, FirstTokenMS: int64Pointer(120), AttemptCount: 1, CreatedAt: time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	stats, err := repository.ListDailyStats(context.Background(), time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListDailyStats: %v", err)
	}
	var globalAverage float64
	found := false
	for _, stat := range stats {
		if stat.DimensionType == DimensionGlobal && stat.DimensionID == GlobalDimensionID {
			globalAverage = stat.AverageFirstTokenMS
			found = true
			break
		}
	}
	if !found || globalAverage != 80 {
		t.Fatalf("daily stats global first token average = %f (found=%v), want 80", globalAverage, found)
	}
}

func TestPercentileReturnsNearestRankQuantiles(t *testing.T) {
	if value := percentile(nil, 0.50); value != nil {
		t.Fatalf("percentile(nil) = %v, want nil", *value)
	}
	single := int64(42)
	if value := percentile([]int64{single}, 0.95); value == nil || *value != single {
		t.Fatalf("percentile(single) = %v, want %d", value, single)
	}
	values := []int64{100, 10, 90, 20, 80, 30, 70, 40, 60, 50}
	if value := percentile(values, 0.50); value == nil || *value != 50 {
		t.Fatalf("p50 = %v, want 50", value)
	}
	if value := percentile(values, 0.95); value == nil || *value != 100 {
		t.Fatalf("p95 = %v, want 100", value)
	}
	if value := percentile(values, 0.00); value == nil || *value != 10 {
		t.Fatalf("p0 = %v, want 10", value)
	}
}

func TestRepositoryMonitoringSummaryComputesFirstTokenQuantiles(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for index, firstToken := range []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		record := RequestRecord{
			RequestID: "qt-" + strconv.Itoa(index), Endpoint: "/v1/chat/completions", ModelID: "model-a",
			HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true,
			DurationMS: 200, FirstTokenMS: int64Pointer(firstToken), AttemptCount: 1,
			CreatedAt: time.Date(2026, 8, 3, 1+index, 0, 0, 0, time.UTC),
		}
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	query, err := NewMonitoringQuery(now, MonitoringRange24Hours, MonitoringFilter{})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	snapshot, err := repository.MonitoringSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("MonitoringSummary: %v", err)
	}
	if snapshot.Summary.FirstTokenP50MS == nil || *snapshot.Summary.FirstTokenP50MS != 50 {
		t.Fatalf("summary TTFT p50 = %v, want 50", snapshot.Summary.FirstTokenP50MS)
	}
	if snapshot.Summary.FirstTokenP95MS == nil || *snapshot.Summary.FirstTokenP95MS != 100 {
		t.Fatalf("summary TTFT p95 = %v, want 100", snapshot.Summary.FirstTokenP95MS)
	}
}

func TestRepositoryMonitoringSummaryComputesFirstTokenQuantilesForDailyWindow(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for index, firstToken := range []int64{10, 20, 30, 40} {
		record := RequestRecord{
			RequestID: "qd-" + strconv.Itoa(index), Endpoint: "/v1/chat/completions", ModelID: "model-a",
			HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true,
			DurationMS: 200, FirstTokenMS: int64Pointer(firstToken), AttemptCount: 1,
			CreatedAt: time.Date(2026, 8, 1, index, 0, 0, 0, time.UTC),
		}
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	query, err := NewMonitoringQuery(now, MonitoringRange30Days, MonitoringFilter{})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	snapshot, err := repository.MonitoringSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("MonitoringSummary: %v", err)
	}
	// Four samples [10,20,30,40]: p50 = ceil(0.5*4)-1 = 1 -> 20, p95 = 4 -> 40.
	if snapshot.Summary.FirstTokenP50MS == nil || *snapshot.Summary.FirstTokenP50MS != 20 {
		t.Fatalf("summary TTFT p50 = %v, want 20", snapshot.Summary.FirstTokenP50MS)
	}
	if snapshot.Summary.FirstTokenP95MS == nil || *snapshot.Summary.FirstTokenP95MS != 40 {
		t.Fatalf("summary TTFT p95 = %v, want 40", snapshot.Summary.FirstTokenP95MS)
	}
}

func TestRepositoryMonitoringSummaryOmitsFirstTokenQuantilesWithoutStreams(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for _, record := range []RequestRecord{
		{RequestID: "no-token-1", Endpoint: "/v1/models", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, DurationMS: 50, AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)},
		{RequestID: "no-token-2", Endpoint: "/v1/chat/completions", ModelID: "model-a", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess, IsStream: true, DurationMS: 50, AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	query, err := NewMonitoringQuery(now, MonitoringRange24Hours, MonitoringFilter{})
	if err != nil {
		t.Fatalf("NewMonitoringQuery: %v", err)
	}
	snapshot, err := repository.MonitoringSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("MonitoringSummary: %v", err)
	}
	if snapshot.Summary.FirstTokenP50MS != nil || snapshot.Summary.FirstTokenP95MS != nil {
		t.Fatalf("TTFT quantiles present without samples: p50=%v p95=%v", snapshot.Summary.FirstTokenP50MS, snapshot.Summary.FirstTokenP95MS)
	}
	encoded, err := json.Marshal(snapshot.Summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if bytes.Contains(encoded, []byte("first_token_p50_ms")) || bytes.Contains(encoded, []byte("first_token_p95_ms")) {
		t.Fatalf("empty TTFT quantiles leaked into JSON: %s", encoded)
	}
}

func seriesPoint(series []MonitoringSeriesPoint, bucket string) MonitoringSeriesPoint {
	for _, point := range series {
		if point.Bucket == bucket {
			return point
		}
	}
	return MonitoringSeriesPoint{Bucket: bucket}
}
