package observability

import (
	"context"
	"net/http"
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

func seriesPoint(series []MonitoringSeriesPoint, bucket string) MonitoringSeriesPoint {
	for _, point := range series {
		if point.Bucket == bucket {
			return point
		}
	}
	return MonitoringSeriesPoint{Bucket: bucket}
}
