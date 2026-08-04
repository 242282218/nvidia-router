package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/clock"
)

// TestDeleteDailyStatsBeforePrunesOldAggregates locks in the cleanup path that
// daily_stats previously lacked entirely: the table grew as day x dimension with
// no upper bound.
func TestDeleteDailyStatsBeforePrunesOldAggregates(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)

	old := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	for _, record := range []RequestRecord{
		{RequestID: "old-1", Endpoint: "/v1/models", ModelID: "m1", HTTPStatus: 200, Outcome: OutcomeSuccess, AttemptCount: 1, CreatedAt: old},
		{RequestID: "new-1", Endpoint: "/v1/models", ModelID: "m1", HTTPStatus: 200, Outcome: OutcomeSuccess, AttemptCount: 1, CreatedAt: recent},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	before := countDailyStats(t, db)
	if before < 4 {
		t.Fatalf("expected aggregates for both days, got %d rows", before)
	}

	deleted, err := repository.DeleteDailyStatsBefore(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DeleteDailyStatsBefore: %v", err)
	}
	if deleted == 0 {
		t.Fatal("DeleteDailyStatsBefore deleted nothing")
	}

	var remainingDays []string
	rows, err := db.Query("SELECT DISTINCT day FROM daily_stats ORDER BY day")
	if err != nil {
		t.Fatalf("query days: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			t.Fatalf("scan day: %v", err)
		}
		remainingDays = append(remainingDays, day)
	}
	if len(remainingDays) != 1 || remainingDays[0] != "2026-07-29" {
		t.Fatalf("remaining days = %v, want only 2026-07-29", remainingDays)
	}
}

// TestDeleteDailyStatsBeforeKeepsBoundaryDay verifies the cutoff is exclusive so
// the boundary day's aggregates survive, matching the request-log semantics.
func TestDeleteDailyStatsBeforeKeepsBoundaryDay(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	day := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	if err := repository.Record(context.Background(), RequestRecord{
		RequestID: "boundary", Endpoint: "/v1/models", HTTPStatus: 200,
		Outcome: OutcomeSuccess, AttemptCount: 1, CreatedAt: day,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	deleted, err := repository.DeleteDailyStatsBefore(context.Background(), day)
	if err != nil {
		t.Fatalf("DeleteDailyStatsBefore: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (boundary day must survive)", deleted)
	}
	if got := countDailyStats(t, db); got == 0 {
		t.Fatal("boundary day aggregates were deleted")
	}
}

// TestCleanupPrunesBothTables verifies the worker drives both retention windows,
// and that the daily_stats cutoff is the longer one.
func TestCleanupPrunesBothTables(t *testing.T) {
	logs := make(chan time.Time, 1)
	stats := make(chan time.Time, 1)
	repository := &twoTableCleanupStub{logs: logs, stats: stats}
	now := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	worker := newCleanupWorker(repository, cleanupSettingsStub{}, func() time.Time { return now },
		func(context.Context, time.Duration) bool { return true }, nil)

	worker.cleanup(context.Background())

	logCutoff := <-logs
	statsCutoff := <-stats
	wantLogCutoff := now.AddDate(0, 0, -DefaultRequestLogRetentionDays)
	if !logCutoff.Equal(wantLogCutoff) {
		t.Fatalf("request log cutoff = %s, want %s", logCutoff, wantLogCutoff)
	}
	wantStatsCutoff := now.AddDate(0, 0, -DailyStatsRetentionDays)
	if !statsCutoff.Equal(wantStatsCutoff) {
		t.Fatalf("daily stats cutoff = %s, want %s", statsCutoff, wantStatsCutoff)
	}
	if !statsCutoff.Before(logCutoff) {
		t.Fatal("daily stats cutoff must be older than the request log cutoff")
	}
}

type twoTableCleanupStub struct {
	logs  chan time.Time
	stats chan time.Time
}

func (s *twoTableCleanupStub) DeleteRequestLogsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.logs <- cutoff
	return 0, nil
}

func (s *twoTableCleanupStub) DeleteDailyStatsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.stats <- cutoff
	return 0, nil
}

// TestStreamUsageCaptureUsesBoundedTail is the behavioural point of the tail
// buffer: a stream far larger than usageCaptureLimit previously dropped its
// usage entirely (the writer reset the buffer on overflow). The tail keeps the
// trailing event, so usage is still recorded, while holding far less heap.
func TestStreamUsageCaptureUsesBoundedTail(t *testing.T) {
	var recorded *RequestRecord
	recorder := recorderFunc(func(_ context.Context, record RequestRecord) error {
		recorded = &record
		return nil
	})

	// Build a stream well past usageCaptureLimit, ending with the usage event.
	filler := "data: " + strings.Repeat("x", 900) + "\n\n"
	repeats := (usageCaptureLimit / len(filler)) + 32
	handler := HTTPMiddleware(recorder, clock.RealClock{}, discardLogger(), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		for i := 0; i < repeats; i++ {
			_, _ = writer.Write([]byte(filler))
		}
		_, _ = writer.Write([]byte(`data: {"usage":{"prompt_tokens":41,"completion_tokens":7}}` + "\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if recorded == nil {
		t.Fatal("no request record produced")
	}
	if recorded.PromptTokens == nil || *recorded.PromptTokens != 41 {
		t.Fatalf("prompt tokens = %v, want 41 (tail capture must survive a long stream)", recorded.PromptTokens)
	}
	if recorded.CompletionTokens == nil || *recorded.CompletionTokens != 7 {
		t.Fatalf("completion tokens = %v, want 7", recorded.CompletionTokens)
	}
	// The client must still receive the whole stream regardless of what the
	// middleware retained.
	if response.Body.Len() < usageCaptureLimit {
		t.Fatalf("client body = %d bytes, want the full stream forwarded", response.Body.Len())
	}
}

// TestNonStreamJSONStillCapturesWholeBody guards that the tail path did not
// change JSON handling: usage can appear anywhere in a JSON object, so the full
// body is still required.
func TestNonStreamJSONStillCapturesWholeBody(t *testing.T) {
	var recorded *RequestRecord
	recorder := recorderFunc(func(_ context.Context, record RequestRecord) error {
		recorded = &record
		return nil
	})
	// usage first, followed by a large trailing field, so a tail-only buffer
	// would miss it.
	payload, err := json.Marshal(map[string]any{
		"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 3},
		"padding": strings.Repeat("y", 128<<10),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, discardLogger(), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(payload)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if recorded == nil {
		t.Fatal("no request record produced")
	}
	if recorded.PromptTokens == nil || *recorded.PromptTokens != 11 {
		t.Fatalf("prompt tokens = %v, want 11", recorded.PromptTokens)
	}
}

func countDailyStats(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM daily_stats").Scan(&count); err != nil {
		t.Fatalf("count daily_stats: %v", err)
	}
	return count
}

type recorderFunc func(context.Context, RequestRecord) error

func (f recorderFunc) Record(ctx context.Context, record RequestRecord) error {
	return f(ctx, record)
}
