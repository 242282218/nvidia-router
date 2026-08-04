package app

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/tests/mocknvidia"
)

func TestAppRecordsRequestMetadataAndFourDimensions(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{
		Status:  http.StatusOK,
		Body:    `{"id":"chat-1","choices":[{}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		Headers: http.Header{"X-Request-Id": []string{"upstream-safe-id"}},
	})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", io.NopCloser(strings.NewReader(chatBody("public-model"))))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	requestID := response.Header.Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("response request ID is empty")
	}
	// The request recorder buffers and flushes asynchronously; force a flush so
	// the in-memory queue lands in SQLite before the test queries it.
	if err := application.FlushObservability(context.Background()); err != nil {
		t.Fatalf("FlushObservability: %v", err)
	}

	var stored struct {
		requestID         string
		endpoint          string
		modelID           sql.NullString
		accessKeyID       sql.NullInt64
		nvidiaKeyID       sql.NullInt64
		status            int
		outcome           string
		queueMS           int64
		firstByteMS       sql.NullInt64
		durationMS        int64
		attemptCount      int
		promptTokens      sql.NullInt64
		completionTokens  sql.NullInt64
		upstreamRequestID sql.NullString
	}
	err = application.db.QueryRow(`
		SELECT request_id, endpoint, model_id, access_key_id, nvidia_key_id,
		       http_status, outcome, queue_ms, first_byte_ms, duration_ms,
		       attempt_count, prompt_tokens, completion_tokens, upstream_request_id
		FROM request_logs
	`).Scan(
		&stored.requestID, &stored.endpoint, &stored.modelID, &stored.accessKeyID, &stored.nvidiaKeyID,
		&stored.status, &stored.outcome, &stored.queueMS, &stored.firstByteMS, &stored.durationMS,
		&stored.attemptCount, &stored.promptTokens, &stored.completionTokens, &stored.upstreamRequestID,
	)
	if err != nil {
		t.Fatalf("query request log: %v", err)
	}
	if stored.requestID != requestID || stored.endpoint != "/v1/chat/completions" || stored.modelID.String != "public-model" {
		t.Fatalf("request identity = %#v", stored)
	}
	if !stored.accessKeyID.Valid || !stored.nvidiaKeyID.Valid || stored.status != http.StatusOK || stored.outcome != "success" {
		t.Fatalf("request dimensions/status = %#v", stored)
	}
	if stored.firstByteMS.Valid || stored.attemptCount != 1 || stored.queueMS < 0 || stored.durationMS < 0 {
		t.Fatalf("request timing/attempts = %#v", stored)
	}
	if stored.promptTokens.Int64 != 3 || stored.completionTokens.Int64 != 2 || stored.upstreamRequestID.String != "upstream-safe-id" {
		t.Fatalf("request usage/upstream ID = %#v", stored)
	}

	wantDimensions := map[string]string{
		"global":     "all",
		"model":      "public-model",
		"nvidia_key": int64String(stored.nvidiaKeyID.Int64),
		"access_key": int64String(stored.accessKeyID.Int64),
	}
	for dimensionType, dimensionID := range wantDimensions {
		var requests, successes, failures, attempts, prompt, completion int64
		if err := application.db.QueryRow(`
			SELECT request_count, success_count, failure_count, total_attempts, prompt_tokens, completion_tokens
			FROM daily_stats WHERE dimension_type = ? AND dimension_id = ?
		`, dimensionType, dimensionID).Scan(&requests, &successes, &failures, &attempts, &prompt, &completion); err != nil {
			t.Fatalf("query %s stats: %v", dimensionType, err)
		}
		if requests != 1 || successes != 1 || failures != 0 || attempts != 1 || prompt != 3 || completion != 2 {
			t.Fatalf("%s stats = %d/%d/%d/%d/%d/%d", dimensionType, requests, successes, failures, attempts, prompt, completion)
		}
	}
}

func TestAppRecordsStreamingFirstByteAndFullConnectionDuration(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, SSE: []mocknvidia.SSEChunk{
		{Delay: 30 * time.Millisecond, Data: "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"},
		{Delay: 60 * time.Millisecond, Data: "data: [DONE]\n\n"},
	}})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	status, _ := postChat(t, server.URL, accessToken, `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if err := application.FlushObservability(context.Background()); err != nil {
		t.Fatalf("FlushObservability: %v", err)
	}
	var firstByteMS sql.NullInt64
	var durationMS int64
	if err := application.db.QueryRow("SELECT first_byte_ms, duration_ms FROM request_logs").Scan(&firstByteMS, &durationMS); err != nil {
		t.Fatalf("query stream timing: %v", err)
	}
	if !firstByteMS.Valid || firstByteMS.Int64 < 20 {
		t.Fatalf("first_byte_ms = %#v, want measured body latency", firstByteMS)
	}
	if durationMS < firstByteMS.Int64+40 {
		t.Fatalf("duration_ms = %d, first_byte_ms = %d; full stream was not measured", durationMS, firstByteMS.Int64)
	}
}

func TestAppStartupSkipsCleanupAndCloseWaitsForWorker(t *testing.T) {
	db := openAppDatabase(t)
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id      string
		created time.Time
	}{
		{id: "expired", created: now.AddDate(0, 0, -30).Add(-time.Nanosecond)},
		{id: "boundary", created: now.AddDate(0, 0, -30)},
	} {
		if _, err := db.Exec(`
			INSERT INTO request_logs(request_id, endpoint, http_status, outcome, is_stream, duration_ms, attempt_count, created_at)
			VALUES (?, '/v1/models', 200, 'success', 0, 1, 0, ?)
		`, item.id, item.created.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", item.id, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO daily_stats(day, dimension_type, dimension_id, request_count)
		VALUES ('2026-06-01', 'global', 'all', 1)
	`); err != nil {
		t.Fatalf("insert daily stats: %v", err)
	}

	application, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), TempDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: fixedAppClock{now: now},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The cleanup worker must not sweep on startup: a retention-window DELETE on
	// the single shared connection would stall live traffic. Expired rows stay
	// until the first scheduled UTC 03:00 pass.
	time.Sleep(100 * time.Millisecond)
	var expired, boundary, stats int
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE request_id = 'expired'").Scan(&expired); err != nil {
		t.Fatalf("query expired: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE request_id = 'boundary'").Scan(&boundary); err != nil {
		t.Fatalf("query boundary: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM daily_stats").Scan(&stats); err != nil {
		t.Fatalf("query daily stats: %v", err)
	}
	if expired != 1 || boundary != 1 || stats != 1 {
		t.Fatalf("expired/boundary/stats = %d/%d/%d, want 1/1/1 before first scheduled cleanup", expired, boundary, stats)
	}

	done := application.cleanupDone
	if err := application.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("Close returned before cleanup worker stopped")
	}
}

type fixedAppClock struct{ now time.Time }

func (c fixedAppClock) Now() time.Time                            { return c.now }
func (fixedAppClock) NewTimer(duration time.Duration) *time.Timer { return time.NewTimer(duration) }
func (fixedAppClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}

func int64String(value int64) string { return strconv.FormatInt(value, 10) }

var _ clock.Clock = fixedAppClock{}
