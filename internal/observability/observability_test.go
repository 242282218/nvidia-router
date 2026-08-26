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
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/database"
	"nvidia-router/internal/runtimeconfig"
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

func TestRequestStateRecordsRequestedCapabilitiesWithoutPayload(t *testing.T) {
	ctx, state := WithRequestState(context.Background())
	SetRequestedCapabilities(ctx, true, true, false)

	snapshot := state.Snapshot()
	if snapshot.RequestedCapabilities != "vision,tools" {
		t.Fatalf("requested capabilities = %q, want vision,tools", snapshot.RequestedCapabilities)
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

func TestRepositoryRecordAggregatesFirstByteOnlyWhenKnown(t *testing.T) {
	db := openObservabilityDB(t)
	firstByte := int64(80)
	record := RequestRecord{
		RequestID: "first-byte", HTTPStatus: http.StatusOK, Outcome: OutcomeSuccess,
		DurationMS: 100, FirstByteMS: &firstByte, AttemptCount: 1,
		CreatedAt: time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var total, count int64
	if err := db.QueryRow(`
		SELECT total_first_byte_ms, first_byte_count
		FROM daily_stats WHERE dimension_type = 'global' AND dimension_id = 'all'
	`).Scan(&total, &count); err != nil {
		t.Fatalf("read first byte aggregate: %v", err)
	}
	if total != 80 || count != 1 {
		t.Fatalf("first byte aggregate = %d/%d, want 80/1", total, count)
	}
}

func TestRepositoryRecordStoresAndAggregatesFirstToken(t *testing.T) {
	db := openObservabilityDB(t)
	firstToken := int64(120)
	record := RequestRecord{
		RequestID: "first-token", Endpoint: "/v1/chat/completions", HTTPStatus: http.StatusOK,
		Outcome: OutcomeSuccess, IsStream: true, DurationMS: 200, FirstTokenMS: &firstToken,
		AttemptCount: 1, CreatedAt: time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var stored sql.NullInt64
	if err := db.QueryRow("SELECT first_token_ms FROM request_logs WHERE request_id = ?", record.RequestID).Scan(&stored); err != nil {
		t.Fatalf("read first token from request log: %v", err)
	}
	if !stored.Valid || stored.Int64 != firstToken {
		t.Fatalf("stored first_token_ms = %#v, want %d", stored, firstToken)
	}
	var total, count int64
	if err := db.QueryRow(`
		SELECT total_first_token_ms, first_token_count
		FROM daily_stats WHERE dimension_type = 'global' AND dimension_id = 'all'
	`).Scan(&total, &count); err != nil {
		t.Fatalf("read first token aggregate: %v", err)
	}
	if total != 120 || count != 1 {
		t.Fatalf("first token aggregate = %d/%d, want 120/1", total, count)
	}
}

func TestRepositoryRecordStoresReasoningObservability(t *testing.T) {
	db := openObservabilityDB(t)
	reasoningChars := int64(340)
	record := RequestRecord{
		RequestID: "reasoning-obs", Endpoint: "/v1/chat/completions", HTTPStatus: http.StatusOK,
		Outcome: OutcomeSuccess, IsStream: true, DurationMS: 300, AttemptCount: 1,
		ReasoningRequested: true, ReasoningWireFields: "reasoning_effort,thinking",
		ReasoningPresent: true, ReasoningChars: &reasoningChars, StreamDone: true,
		RouteMode: "built-in", CreatedAt: time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var requested, present, done int
	var wireFields, routeMode sql.NullString
	var chars sql.NullInt64
	if err := db.QueryRow(`
		SELECT reasoning_requested, reasoning_wire_fields, reasoning_present,
		       reasoning_chars, stream_done, route_mode
		FROM request_logs WHERE request_id = ?
	`, record.RequestID).Scan(&requested, &wireFields, &present, &chars, &done, &routeMode); err != nil {
		t.Fatalf("read reasoning observability: %v", err)
	}
	if requested != 1 || present != 1 || done != 1 {
		t.Fatalf("reasoning booleans = requested %d present %d done %d, want 1/1/1", requested, present, done)
	}
	if !wireFields.Valid || wireFields.String != "reasoning_effort,thinking" {
		t.Fatalf("wire fields = %#v, want reasoning_effort,thinking", wireFields)
	}
	if !chars.Valid || chars.Int64 != reasoningChars {
		t.Fatalf("reasoning chars = %#v, want %d", chars, reasoningChars)
	}
	if !routeMode.Valid || routeMode.String != "built-in" {
		t.Fatalf("route mode = %#v, want built-in", routeMode)
	}
}

func TestRepositoryRecordStoresReasoningLevels(t *testing.T) {
	db := openObservabilityDB(t)
	record := RequestRecord{
		RequestID: "reasoning-levels", Endpoint: "/v1/chat/completions", HTTPStatus: http.StatusOK,
		Outcome: OutcomeSuccess, DurationMS: 80, AttemptCount: 1,
		ReasoningRequestedLevel: "high", ReasoningEffectiveLevel: "medium",
		CreatedAt: time.Date(2026, 8, 18, 1, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var requested, effective sql.NullString
	if err := db.QueryRow(`
		SELECT reasoning_requested_level, reasoning_effective_level
		FROM request_logs WHERE request_id = ?
	`, record.RequestID).Scan(&requested, &effective); err != nil {
		t.Fatalf("read reasoning levels: %v", err)
	}
	if !requested.Valid || requested.String != record.ReasoningRequestedLevel {
		t.Fatalf("requested level = %#v, want %q", requested, record.ReasoningRequestedLevel)
	}
	if !effective.Valid || effective.String != record.ReasoningEffectiveLevel {
		t.Fatalf("effective level = %#v, want %q", effective, record.ReasoningEffectiveLevel)
	}
}

func TestRepositoryRecordStoresRequestedCapabilities(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	record := RequestRecord{
		RequestID: "requested-capabilities", Endpoint: "/v1/chat/completions", ModelID: "chat-model",
		HTTPStatus: http.StatusNotImplemented, Outcome: OutcomeFailure, DurationMS: 10, AttemptCount: 1,
		RequestedCapabilities: "tools,reasoning", CreatedAt: time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC),
	}
	if err := repository.Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var capabilities sql.NullString
	if err := db.QueryRow("SELECT requested_capabilities FROM request_logs WHERE request_id = ?", record.RequestID).Scan(&capabilities); err != nil {
		t.Fatalf("read requested capabilities: %v", err)
	}
	if !capabilities.Valid || capabilities.String != record.RequestedCapabilities {
		t.Fatalf("requested capabilities = %#v, want %q", capabilities, record.RequestedCapabilities)
	}
}

func TestRepositoryRecordStoresReasoningAbsentAsDefaults(t *testing.T) {
	db := openObservabilityDB(t)
	record := RequestRecord{
		RequestID: "reasoning-absent", Endpoint: "/v1/chat/completions", HTTPStatus: http.StatusOK,
		Outcome: OutcomeSuccess, DurationMS: 50, AttemptCount: 1, IsStream: false,
		CreatedAt: time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var requested, present, done int
	var wireFields, chars, routeMode any
	if err := db.QueryRow(`
		SELECT reasoning_requested, reasoning_wire_fields, reasoning_present,
		       reasoning_chars, stream_done, route_mode
		FROM request_logs WHERE request_id = ?
	`, record.RequestID).Scan(&requested, &wireFields, &present, &chars, &done, &routeMode); err != nil {
		t.Fatalf("read reasoning observability: %v", err)
	}
	if requested != 0 || present != 0 || done != 0 {
		t.Fatalf("reasoning booleans = requested %d present %d done %d, want 0/0/0", requested, present, done)
	}
	if wireFields != nil || chars != nil || routeMode != nil {
		t.Fatalf("optional reasoning fields should be NULL, got %v / %v / %v", wireFields, chars, routeMode)
	}
}

func TestHTTPMiddlewareRecordsFirstTokenMSForStream(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		SetModel(request.Context(), "chat-model", true)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		SetFirstTokenAt(request.Context(), time.Now())
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if recorder.record.FirstTokenMS == nil {
		t.Fatal("first_token_ms not recorded for a stream that produced a token")
	}
	if *recorder.record.FirstTokenMS < 0 {
		t.Fatalf("first_token_ms = %d, want >= 0", *recorder.record.FirstTokenMS)
	}
}

func TestHTTPMiddlewareLeavesFirstTokenMSNilWhenNoToken(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		SetModel(request.Context(), "chat-model", true)
		writer.WriteHeader(http.StatusBadGateway)
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if recorder.record.FirstTokenMS != nil {
		t.Fatalf("first_token_ms = %d, want nil for a stream that never produced a token", *recorder.record.FirstTokenMS)
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

func TestRepositoryDeleteRequestLogsBeforeMillisecondBoundary(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, item := range []struct {
		id string
		at time.Time
	}{
		{"fraction-old", time.Date(2026, 6, 30, 3, 0, 0, 949000000, time.UTC)},
		{"fraction-boundary", time.Date(2026, 6, 30, 3, 0, 0, 950000000, time.UTC)},
		{"second-new", time.Date(2026, 6, 30, 3, 0, 1, 0, time.UTC)},
	} {
		code := "test_error"
		if err := repository.Record(context.Background(), RequestRecord{
			RequestID: item.id, Endpoint: "/v1/models", HTTPStatus: 500, Outcome: OutcomeFailure,
			ErrorCode: &code, DurationMS: 1, AttemptCount: 1, CreatedAt: item.at,
		}); err != nil {
			t.Fatalf("Record %s: %v", item.id, err)
		}
	}

	deleted, err := repository.DeleteRequestLogsBefore(context.Background(), time.Date(2026, 6, 30, 3, 0, 0, 950000000, time.UTC))
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
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

func TestCleanupWorkerDoesNotRunAtStartupAndSchedulesNext(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{called: make(chan time.Time, 1)}
	waitStarted := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	worker := newCleanupWorker(repository, nil, func() time.Time { return now }, func(ctx context.Context, duration time.Duration) bool {
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
	case duration := <-waitStarted:
		want := time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC).Sub(now.UTC())
		if duration != want {
			t.Fatalf("next cleanup delay = %s, want %s", duration, want)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not schedule next UTC 03:00 cleanup")
	}
	select {
	case cutoff := <-repository.called:
		t.Fatalf("cleanup ran at startup with cutoff %s", cutoff)
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func TestCleanupWorkerRunsWhenScheduledWaitElapses(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{called: make(chan time.Time, 1)}
	waitCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	worker := newCleanupWorker(repository, nil, func() time.Time { return now }, func(ctx context.Context, _ time.Duration) bool {
		waitCalls++
		if waitCalls == 1 {
			return true
		}
		cancel()
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
		t.Fatal("cleanup was not run after scheduled wait elapsed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

// TestCleanupWorkerHonoursOperatorTunedRetentionDays verifies that a valid
// operator setting is read from the live runtime snapshot while values below
// the one-month safety floor fall back to the documented default.
func TestCleanupWorkerHonoursOperatorTunedRetentionDays(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{called: make(chan time.Time, 1)}
	settings := cleanupSettingsStub{snapshot: runtimeconfig.Snapshot{RequestLogRetentionDays: 7}}
	ctx, cancel := context.WithCancel(context.Background())
	worker := newCleanupWorker(repository, settings, func() time.Time { return now }, func(_ context.Context, _ time.Duration) bool {
		return true
	}, nil)
	go func() {
		worker.Run(ctx)
	}()

	select {
	case cutoff := <-repository.called:
		want := now.UTC().AddDate(0, 0, -DefaultRequestLogRetentionDays)
		if !cutoff.Equal(want) {
			t.Fatalf("invalid tuned cleanup cutoff = %s, want %s (retention floor=%d)", cutoff, want, DefaultRequestLogRetentionDays)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup did not run for the tuned retention window")
	}
	cancel()
}

// TestCleanupWorkerFallsBackToDefaultWhenSettingsMissing verifies that an
// empty/misconfigured snapshot falls back to the documented default rather
// than the naive AddDate(0,0,0) "delete everything" path the previous
// constant-only implementation avoided by construction (audit B5).
func TestCleanupWorkerFallsBackToDefaultWhenSettingsMissing(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{called: make(chan time.Time, 1)}
	settings := cleanupSettingsStub{snapshot: runtimeconfig.Snapshot{RequestLogRetentionDays: 0}}
	ctx, cancel := context.WithCancel(context.Background())
	worker := newCleanupWorker(repository, settings, func() time.Time { return now }, func(_ context.Context, _ time.Duration) bool {
		return true
	}, nil)
	go func() {
		worker.Run(ctx)
	}()

	select {
	case cutoff := <-repository.called:
		want := now.UTC().AddDate(0, 0, -DefaultRequestLogRetentionDays)
		if !cutoff.Equal(want) {
			t.Fatalf("fallback cleanup cutoff = %s, want %s (retention=%d)", cutoff, want, DefaultRequestLogRetentionDays)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup did not run for the fallback retention window")
	}
	cancel()
}

type cleanupSettingsStub struct {
	snapshot runtimeconfig.Snapshot
}

func (s cleanupSettingsStub) Snapshot() runtimeconfig.Snapshot { return s.snapshot }

type cleanupRepositoryStub struct {
	called      chan time.Time
	statsCalled chan time.Time
}

func (s *cleanupRepositoryStub) DeleteRequestLogsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.called <- cutoff
	return 0, nil
}

func (s *cleanupRepositoryStub) DeleteDailyStatsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	if s.statsCalled != nil {
		s.statsCalled <- cutoff
	}
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

func TestRepositoryListRecentErrorsOrdersMillisecondTimestamps(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	for _, item := range []struct {
		id string
		at time.Time
	}{
		{"older-whole", time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)},
		{"later-fraction", time.Date(2026, 7, 30, 0, 0, 0, 900000000, time.UTC)},
		{"latest-whole", time.Date(2026, 7, 30, 0, 0, 1, 0, time.UTC)},
	} {
		code := "test_error"
		if err := repository.Record(context.Background(), RequestRecord{
			RequestID: item.id, Endpoint: "/v1/models", HTTPStatus: 500, Outcome: OutcomeFailure,
			ErrorCode: &code, DurationMS: 1, AttemptCount: 1, CreatedAt: item.at,
		}); err != nil {
			t.Fatalf("Record %s: %v", item.id, err)
		}
	}

	items, err := repository.ListRecentErrors(context.Background(), 3)
	if err != nil {
		t.Fatalf("ListRecentErrors: %v", err)
	}
	want := []string{"latest-whole", "later-fraction", "older-whole"}
	if len(items) != len(want) {
		t.Fatalf("recent error count = %d, want %d", len(items), len(want))
	}
	for index, item := range items {
		if item.RequestID != want[index] {
			t.Fatalf("recent error %d = %q, want %q", index, item.RequestID, want[index])
		}
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

func TestTrackingWriterRetainsSSEAndDropsUncapturableBodies(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		content      string
		stream       bool
		payload      []byte
		wantRetained bool
		wantTail     bool // body keeps a tail window (captureComplete still true)
	}{
		{name: "sse", endpoint: "/v1/chat/completions", content: "text/event-stream", stream: true, payload: []byte("data: {\"usage\":{\"prompt_tokens\":1}}\n\n"), wantRetained: true},
		{name: "audio", endpoint: "/v1/audio/speech", content: "audio/mpeg", payload: bytes.Repeat([]byte("audio"), 32)},
		// A large JSON response is no longer dropped wholesale: its usage block
		// sits at the end of the body, so the tail window is retained and
		// parseUsage can still extract prompt/completion tokens (audit #19/N4).
		{name: "large json", endpoint: "/v1/chat/completions", content: "application/json", payload: bytes.Repeat([]byte("x"), usageCaptureLimit+1), wantTail: true},
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
			if test.wantRetained {
				if !tracking.captureComplete || tracking.body.Len() == 0 {
					t.Fatalf("SSE body retained length/completeness = %d/%v, want >0/true", tracking.body.Len(), tracking.captureComplete)
				}
				return
			}
			if test.wantTail {
				if tracking.body.Len() == 0 || !tracking.captureComplete {
					t.Fatalf("tail body length/completeness = %d/%v, want >0/true", tracking.body.Len(), tracking.captureComplete)
				}
				return
			}
			if tracking.body.Len() != 0 || tracking.captureComplete {
				t.Fatalf("retained body length/completeness = %d/%v, want 0/false", tracking.body.Len(), tracking.captureComplete)
			}
		})
	}
}

func TestHTTPMiddlewareExtractsUsageFromSSETail(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		SetModel(request.Context(), "chat-model", true)
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		for _, event := range []string{
			`data: {"id":"1","choices":[{"delta":{"content":"hi"}}]}`,
			`data: {"id":"1","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7}}`,
			`data: [DONE]`,
		} {
			_, _ = writer.Write([]byte(event + "\n\n"))
			flusher.Flush()
		}
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if recorder.record.PromptTokens == nil || *recorder.record.PromptTokens != 12 || recorder.record.CompletionTokens == nil || *recorder.record.CompletionTokens != 7 {
		t.Fatalf("usage = %v/%v, want 12/7", recorder.record.PromptTokens, recorder.record.CompletionTokens)
	}
}

func TestHTTPMiddlewareExtractsUsageFromResponsesCompletedEvent(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		SetModel(request.Context(), "responses-model", true)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n"))
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if recorder.record.PromptTokens == nil || *recorder.record.PromptTokens != 5 || recorder.record.CompletionTokens == nil || *recorder.record.CompletionTokens != 3 {
		t.Fatalf("usage = %v/%v, want 5/3", recorder.record.PromptTokens, recorder.record.CompletionTokens)
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

func TestHTTPMiddlewareAddsFallbackErrorCodeForUnclassifiedFailure(t *testing.T) {
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if recorder.record.ErrorCode == nil || *recorder.record.ErrorCode != "http_5xx" {
		t.Fatalf("fallback error code = %v, want http_5xx", recorder.record.ErrorCode)
	}
}

func TestHTTPMiddlewareExtractsUsageFromLargeJSONTail(t *testing.T) {
	// Regression for audit #19/N4: a non-stream JSON response larger than the
	// capture limit used to lose its usage entirely (body reset to empty). The
	// tail window now keeps the trailing usage object so long responses still
	// meter tokens.
	recorder := &observabilityRecorder{}
	handler := HTTPMiddleware(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		SetModel(request.Context(), "chat-model", false)
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Pad far past the capture limit, then end with a valid usage object so
		// the retained tail contains it.
		prefix := strings.Repeat("x", usageCaptureLimit+64<<10)
		_, _ = writer.Write([]byte(prefix + `{"usage":{"prompt_tokens":5,"completion_tokens":9}}`))
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if recorder.record.PromptTokens == nil || *recorder.record.PromptTokens != 5 || recorder.record.CompletionTokens == nil || *recorder.record.CompletionTokens != 9 {
		t.Fatalf("usage = %v/%v, want 5/9", recorder.record.PromptTokens, recorder.record.CompletionTokens)
	}
}
