package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/observability"
)

func TestMonitoringSummaryReturnsSnapshotAndUsesDefaultRange(t *testing.T) {
	store := &monitoringStoreStub{
		summary: observability.MonitoringSnapshot{
			Range: observability.MonitoringRange24Hours,
			From:  time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC),
			To:    time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC),
			Summary: observability.MonitoringSummary{
				RequestCount: 2, SuccessCount: 1, FailureCount: 1, SuccessRate: 50,
				AverageDurationMS: 120, AverageFirstByteMS: 80, AverageQueueMS: 10,
				TotalAttempts: 3, PromptTokens: 20, CompletionTokens: 8,
			},
		},
	}
	handler := NewMonitoring(store, fixedAdminClock{now: time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/monitoring/summary", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("summary status = %d: %s", response.Code, response.Body.String())
	}
	if store.summaryQuery.Range != observability.MonitoringRange24Hours {
		t.Fatalf("summary range = %q, want 24h", store.summaryQuery.Range)
	}
	var body struct {
		Data observability.MonitoringSnapshot `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if body.Data.Summary.RequestCount != 2 || body.Data.Summary.SuccessRate != 50 {
		t.Fatalf("summary body = %#v", body.Data.Summary)
	}
}

func TestMonitoringLogsReturnsStablePageEnvelope(t *testing.T) {
	store := &monitoringStoreStub{
		logs: observability.RequestLogsPage{
			Items: []observability.RequestLog{{RequestID: "safe-request", Endpoint: "/v1/models", HTTPStatus: 200, Outcome: observability.OutcomeSuccess, DurationMS: 40, AttemptCount: 1, CreatedAt: "2026-08-03T03:00:00.000Z"}},
			Page:  2, PageSize: 20, Total: 21, HasMore: false,
		},
	}
	handler := NewMonitoring(store, fixedAdminClock{now: time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)})
	request := httptest.NewRequest(http.MethodGet, "/admin/api/monitoring/logs?range=7d&page=2&page_size=20&outcome=success&status=200&search=safe", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("logs status = %d: %s", response.Code, response.Body.String())
	}
	if store.logsQuery.Range != observability.MonitoringRange7Days || store.logsQuery.Page != 2 || store.logsQuery.PageSize != 20 {
		t.Fatalf("logs query = %#v", store.logsQuery)
	}
	if store.logsQuery.Filter.Status == nil || *store.logsQuery.Filter.Status != 200 || store.logsQuery.Filter.Search != "safe" {
		t.Fatalf("logs filters = %#v", store.logsQuery.Filter)
	}
	var body struct {
		Data observability.RequestLogsPage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if body.Data.Page != 2 || body.Data.Total != 21 || body.Data.HasMore {
		t.Fatalf("logs body = %#v", body.Data)
	}
	if strings.Contains(response.Body.String(), "body") || strings.Contains(response.Body.String(), "response") {
		t.Fatalf("logs response contains forbidden field: %s", response.Body.String())
	}
}

func TestMonitoringRejectsInvalidInputs(t *testing.T) {
	handler := NewMonitoring(&monitoringStoreStub{}, fixedAdminClock{now: time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)})
	search := strings.Repeat("x", 129)
	for _, target := range []string{
		"/admin/api/monitoring/summary?range=90d",
		"/admin/api/monitoring/summary?status=99",
		"/admin/api/monitoring/logs?page=0",
		"/admin/api/monitoring/logs?page_size=101",
		"/admin/api/monitoring/logs?access_key_id=0",
		"/admin/api/monitoring/logs?search=" + search,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400", target, response.Code)
		}
	}
}

func TestMonitoringMapsStoreErrorsToGenericResponse(t *testing.T) {
	store := &monitoringStoreStub{summaryErr: errors.New("private database details")}
	handler := NewMonitoring(store, fixedAdminClock{now: time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/monitoring/summary?range=24h", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("store error status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "private database details") {
		t.Fatalf("internal error leaked: %s", response.Body.String())
	}
}

func TestMonitoringRejectsNonGetMethods(t *testing.T) {
	handler := NewMonitoring(&monitoringStoreStub{}, fixedAdminClock{now: time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/api/monitoring/summary", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("POST status = %d, want 404", response.Code)
	}
}

type monitoringStoreStub struct {
	summary      observability.MonitoringSnapshot
	logs         observability.RequestLogsPage
	summaryErr   error
	logsErr      error
	summaryQuery observability.MonitoringQuery
	logsQuery    observability.RequestLogsQuery
}

func (s *monitoringStoreStub) MonitoringSummary(_ context.Context, query observability.MonitoringQuery) (observability.MonitoringSnapshot, error) {
	s.summaryQuery = query
	if s.summaryErr != nil {
		return observability.MonitoringSnapshot{}, s.summaryErr
	}
	return s.summary, nil
}

func (s *monitoringStoreStub) ListRequestLogs(_ context.Context, query observability.RequestLogsQuery) (observability.RequestLogsPage, error) {
	s.logsQuery = query
	if s.logsErr != nil {
		return observability.RequestLogsPage{}, s.logsErr
	}
	return s.logs, nil
}
