package observability

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/database"
)

func TestRequestLogPersistsReasoningSource(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	record := RequestRecord{
		RequestID:          "request-reasoning-source",
		Endpoint:           "/v1/chat/completions",
		HTTPStatus:         200,
		Outcome:            OutcomeSuccess,
		CreatedAt:          time.Now().UTC(),
		ReasoningRequested: true,
		ReasoningSource:    "auto-inject",
	}
	if err := NewRepository(db).Record(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := db.QueryRow("SELECT reasoning_source FROM request_logs WHERE request_id = ?", record.RequestID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "auto-inject" {
		t.Fatalf("reasoning_source = %q, want auto-inject", source)
	}
	query, err := NewMonitoringQuery(time.Now().UTC(), MonitoringRange24Hours, MonitoringFilter{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := NewRepository(db).ListRequestLogs(context.Background(), RequestLogsQuery{MonitoringQuery: query, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ReasoningSource == nil || *page.Items[0].ReasoningSource != "auto-inject" {
		t.Fatalf("monitoring reasoning source = %#v, want auto-inject", page.Items)
	}
}
