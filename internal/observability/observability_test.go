package observability

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/database"
)

func TestRequestRecordContainsOnlyAllowlistedMetadata(t *testing.T) {
	forbidden := map[string]struct{}{
		"Body": {}, "Prompt": {}, "Response": {}, "Content": {},
		"Headers": {}, "Cookie": {}, "Secret": {},
	}
	typ := reflect.TypeOf(RequestRecord{})
	for index := 0; index < typ.NumField(); index++ {
		if _, found := forbidden[typ.Field(index).Name]; found {
			t.Fatalf("RequestRecord contains forbidden field %q", typ.Field(index).Name)
		}
	}
}

func TestRepositoryRecordStoresRequestAndFourDailyDimensions(t *testing.T) {
	db := openObservabilityDB(t)
	accessKeyID := insertAccessKey(t, db)
	nvidiaKeyID := insertNVIDIAKey(t, db)
	promptTokens := int64(12)
	completionTokens := int64(7)
	firstByteMS := int64(45)
	record := RequestRecord{
		RequestID: "req-observability-1", Endpoint: "/v1/chat/completions", ModelID: "chat-model",
		AccessKeyID: &accessKeyID, NVIDIAKeyID: &nvidiaKeyID, HTTPStatus: 200, Outcome: OutcomeSuccess,
		IsStream: true, QueueMS: 9, FirstByteMS: &firstByteMS, DurationMS: 150,
		AttemptCount: 2, PromptTokens: &promptTokens, CompletionTokens: &completionTokens,
		CreatedAt: time.Date(2026, 7, 30, 23, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)),
	}

	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var storedModel string
	var storedAccessKeyID, storedNVIDIAKeyID int64
	var storedFirstByte, storedPromptTokens, storedCompletionTokens sql.NullInt64
	if err := db.QueryRow(`
		SELECT model_id, access_key_id, nvidia_key_id, first_byte_ms, prompt_tokens, completion_tokens
		FROM request_logs WHERE request_id = ?
	`, record.RequestID).Scan(&storedModel, &storedAccessKeyID, &storedNVIDIAKeyID, &storedFirstByte, &storedPromptTokens, &storedCompletionTokens); err != nil {
		t.Fatalf("query request log: %v", err)
	}
	if storedModel != record.ModelID || storedAccessKeyID != accessKeyID || storedNVIDIAKeyID != nvidiaKeyID {
		t.Fatalf("stored dimensions = %q/%d/%d", storedModel, storedAccessKeyID, storedNVIDIAKeyID)
	}
	if storedFirstByte.Int64 != firstByteMS || storedPromptTokens.Int64 != promptTokens || storedCompletionTokens.Int64 != completionTokens {
		t.Fatalf("stored optional metrics = %#v/%#v/%#v", storedFirstByte, storedPromptTokens, storedCompletionTokens)
	}

	wantDimensions := map[string]string{
		DimensionGlobal:    GlobalDimensionID,
		DimensionModel:     record.ModelID,
		DimensionNVIDIAKey: strconv.FormatInt(nvidiaKeyID, 10),
		DimensionAccessKey: strconv.FormatInt(accessKeyID, 10),
	}
	for dimensionType, dimensionID := range wantDimensions {
		var requestCount, successCount, failureCount, durationMS, queueMS, attempts, prompt, completion int64
		if err := db.QueryRow(`
			SELECT request_count, success_count, failure_count, total_duration_ms,
			       total_queue_ms, total_attempts, prompt_tokens, completion_tokens
			FROM daily_stats WHERE day = ? AND dimension_type = ? AND dimension_id = ?
		`, "2026-07-30", dimensionType, dimensionID).Scan(
			&requestCount, &successCount, &failureCount, &durationMS,
			&queueMS, &attempts, &prompt, &completion,
		); err != nil {
			t.Fatalf("query %s stats: %v", dimensionType, err)
		}
		if requestCount != 1 || successCount != 1 || failureCount != 0 || durationMS != 150 || queueMS != 9 || attempts != 2 || prompt != 12 || completion != 7 {
			t.Fatalf("%s aggregate = %d/%d/%d/%d/%d/%d/%d/%d", dimensionType, requestCount, successCount, failureCount, durationMS, queueMS, attempts, prompt, completion)
		}
	}
}

func TestRepositoryRecordKeepsUnknownUsageNullAndAggregatesFailure(t *testing.T) {
	db := openObservabilityDB(t)
	errorCode := "upstream_rate_limited"
	record := RequestRecord{
		RequestID: "req-observability-2", Endpoint: "/v1/responses", ModelID: "chat-model",
		HTTPStatus: 429, Outcome: OutcomeFailure, ErrorCode: &errorCode,
		DurationMS: 25, AttemptCount: 3, CreatedAt: time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var promptTokens, completionTokens sql.NullInt64
	if err := db.QueryRow("SELECT prompt_tokens, completion_tokens FROM request_logs WHERE request_id = ?", record.RequestID).Scan(&promptTokens, &completionTokens); err != nil {
		t.Fatalf("query tokens: %v", err)
	}
	if promptTokens.Valid || completionTokens.Valid {
		t.Fatalf("unknown usage persisted as values: %#v/%#v", promptTokens, completionTokens)
	}
	var failures, successes int64
	if err := db.QueryRow(`SELECT failure_count, success_count FROM daily_stats WHERE day = '2026-07-30' AND dimension_type = 'global' AND dimension_id = 'all'`).Scan(&failures, &successes); err != nil {
		t.Fatalf("query aggregate: %v", err)
	}
	if failures != 1 || successes != 0 {
		t.Fatalf("failure/success = %d/%d", failures, successes)
	}
}

func TestRepositoryRecordRollsBackAllDimensionsOnDuplicateRequestID(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	record := RequestRecord{RequestID: "duplicate", Endpoint: "/v1/models", HTTPStatus: 200, Outcome: OutcomeSuccess, DurationMS: 1, AttemptCount: 1, CreatedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)}
	if err := repository.Record(context.Background(), record); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	if err := repository.Record(context.Background(), record); err == nil {
		t.Fatal("duplicate Record error = nil")
	}
	var requestCount int64
	if err := db.QueryRow(`SELECT request_count FROM daily_stats WHERE day = '2026-07-30' AND dimension_type = 'global' AND dimension_id = 'all'`).Scan(&requestCount); err != nil {
		t.Fatalf("query request count: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("request_count = %d, want 1", requestCount)
	}
}

func TestRepositoryDeleteRequestLogsBeforeKeepsBoundaryAndDailyStats(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, item := range []struct {
		id string
		at time.Time
	}{
		{"old", time.Date(2026, 6, 29, 2, 59, 59, 0, time.UTC)},
		{"boundary", time.Date(2026, 6, 30, 3, 0, 0, 0, time.UTC)},
		{"new", time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), RequestRecord{RequestID: item.id, Endpoint: "/v1/models", HTTPStatus: 200, Outcome: OutcomeSuccess, DurationMS: 1, AttemptCount: 1, CreatedAt: item.at}); err != nil {
			t.Fatalf("Record %s: %v", item.id, err)
		}
	}
	deleted, err := repository.DeleteRequestLogsBefore(context.Background(), time.Date(2026, 6, 30, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	var logs, stats int
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&logs); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM daily_stats").Scan(&stats); err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if logs != 2 || stats != 3 {
		t.Fatalf("logs/stats = %d/%d, want 2/3", logs, stats)
	}
}

func openObservabilityDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(t.TempDir() + "/router.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertAccessKey(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO access_keys (name, key_digest, key_prefix, created_at) VALUES ('test', x'0102', 'nvr_test', '2026-07-30T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert access key: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("access key ID: %v", err)
	}
	return id
}

func insertNVIDIAKey(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO nvidia_keys (ciphertext, nonce, fingerprint, display_prefix, display_suffix, created_at, updated_at)
		VALUES (x'01', x'02', x'03', 'nvap', '7890', '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert NVIDIA key: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("NVIDIA key ID: %v", err)
	}
	return id
}

func TestCleanupWorkerRunsAtStartupAndUsesThirtyDayUTCThreshold(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{called: make(chan time.Time, 1)}
	waitStarted := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	worker := newCleanupWorker(repository, func() time.Time { return now }, func(ctx context.Context, duration time.Duration) bool {
		waitStarted <- duration
		<-ctx.Done()
		return false
	}, nil)
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	select {
	case cutoff := <-repository.called:
		want := now.UTC().AddDate(0, 0, -30)
		if !cutoff.Equal(want) {
			t.Fatalf("cleanup cutoff = %s, want %s", cutoff, want)
		}
	case <-time.After(time.Second):
		t.Fatal("startup cleanup was not run")
	}
	select {
	case duration := <-waitStarted:
		want := time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC).Sub(now.UTC())
		if duration != want {
			t.Fatalf("next cleanup delay = %s, want %s", duration, want)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not schedule next UTC 03:00 cleanup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

type cleanupRepositoryStub struct {
	called chan time.Time
}

func (s *cleanupRepositoryStub) DeleteRequestLogsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.called <- cutoff
	return 0, nil
}

func TestRepositoryListDailyStatsCalculatesSafeAverages(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, record := range []RequestRecord{
		{RequestID: "stats-1", Endpoint: "/v1/models", HTTPStatus: 200, Outcome: OutcomeSuccess, QueueMS: 10, DurationMS: 100, AttemptCount: 1, CreatedAt: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)},
		{RequestID: "stats-2", Endpoint: "/v1/models", HTTPStatus: 500, Outcome: OutcomeFailure, QueueMS: 30, DurationMS: 300, AttemptCount: 3, CreatedAt: time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)},
	} {
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %s: %v", record.RequestID, err)
		}
	}
	stats, err := repository.ListDailyStats(context.Background(), time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListDailyStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats length = %d, want 1", len(stats))
	}
	stat := stats[0]
	if stat.RequestCount != 2 || stat.SuccessCount != 1 || stat.FailureCount != 1 {
		t.Fatalf("counts = %d/%d/%d", stat.RequestCount, stat.SuccessCount, stat.FailureCount)
	}
	if stat.AverageDuration != 200 || stat.AverageQueue != 20 || stat.AverageAttempts != 2 {
		t.Fatalf("averages = %f/%f/%f", stat.AverageDuration, stat.AverageQueue, stat.AverageAttempts)
	}
}

func TestRepositoryListRecentErrorsReturnsOnlyAllowlistedFields(t *testing.T) {
	typ := reflect.TypeOf(RecentError{})
	forbidden := map[string]struct{}{"Message": {}, "Body": {}, "Response": {}, "Headers": {}, "Secret": {}}
	for index := 0; index < typ.NumField(); index++ {
		if _, found := forbidden[typ.Field(index).Name]; found {
			t.Fatalf("RecentError contains forbidden field %q", typ.Field(index).Name)
		}
	}

	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for index := 0; index < 3; index++ {
		code := "fixed_error_" + strconv.Itoa(index)
		model := "model-" + strconv.Itoa(index)
		record := RequestRecord{
			RequestID: "error-" + strconv.Itoa(index), Endpoint: "/v1/chat/completions", ModelID: model,
			HTTPStatus: 500 + index, Outcome: OutcomeFailure, ErrorCode: &code, DurationMS: 10,
			AttemptCount: 1, CreatedAt: time.Date(2026, 7, 30, index, 0, 0, 0, time.UTC),
		}
		if err := repository.Record(context.Background(), record); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	errorsList, err := repository.ListRecentErrors(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListRecentErrors: %v", err)
	}
	if len(errorsList) != 2 || errorsList[0].RequestID != "error-2" || errorsList[1].RequestID != "error-1" {
		t.Fatalf("recent errors = %#v", errorsList)
	}
	if errorsList[0].ErrorCode != "fixed_error_2" || errorsList[0].HTTPStatus != 502 {
		t.Fatalf("latest error = %#v", errorsList[0])
	}
}

type observabilityRecorder struct {
	record RequestRecord
}

func (r *observabilityRecorder) Record(_ context.Context, record RequestRecord) error {
	r.record = record
	return nil
}

func TestHTTPMiddlewareExtractsUsageFromEligibleJSON(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		SetModel(request.Context(), "chat-model", false)
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(`{"usage":{"prompt_tokens":12,"completion_tokens":7}}`))
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if recorder.record.PromptTokens == nil || *recorder.record.PromptTokens != 12 || recorder.record.CompletionTokens == nil || *recorder.record.CompletionTokens != 7 {
		t.Fatalf("usage = %v/%v, want 12/7", recorder.record.PromptTokens, recorder.record.CompletionTokens)
	}
}

func TestTrackingWriterDoesNotRetainSSEAudioOrLargeBodies(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		content  string
		stream   bool
		payload  []byte
	}{
		{name: "sse", endpoint: "/v1/chat/completions", content: "text/event-stream", stream: true, payload: []byte(`data: {"usage":{"prompt_tokens":1}}

`)},
		{name: "audio", endpoint: "/v1/audio/speech", content: "audio/mpeg", payload: bytes.Repeat([]byte("audio"), 32)},
		{name: "large json", endpoint: "/v1/chat/completions", content: "application/json", payload: bytes.Repeat([]byte("x"), usageCaptureLimit+1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := WithRequestState(context.Background())
			SetModel(ctx, "chat-model", test.stream)
			response := httptest.NewRecorder()
			tracking := newTrackingWriter(response, ctx, time.Now(), clock.RealClock{}, test.endpoint)
			tracking.Header().Set("Content-Type", test.content)
			if _, err := tracking.Write(test.payload); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if tracking.body.Len() != 0 || tracking.captureComplete {
				t.Fatalf("retained body length/completeness = %d/%v, want 0/false", tracking.body.Len(), tracking.captureComplete)
			}
		})
	}
}

func TestHTTPMiddlewareSkipsUsageForErrorJSON(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"usage":{"prompt_tokens":12,"completion_tokens":7}}`))
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if recorder.record.PromptTokens != nil || recorder.record.CompletionTokens != nil {
		t.Fatalf("error response usage = %v/%v, want nil/nil", recorder.record.PromptTokens, recorder.record.CompletionTokens)
	}
}
