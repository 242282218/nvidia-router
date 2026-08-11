package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nvidia-router/internal/observability"
)

func TestStatsHandlerReturnsDailyDimensionsAndSafeRecentErrors(t *testing.T) {
	store := &statsStoreStub{
		stats:  []observability.DailyStat{{Day: "2026-07-30", DimensionType: observability.DimensionGlobal, DimensionID: observability.GlobalDimensionID, RequestCount: 2, SuccessCount: 1, FailureCount: 1, AverageDuration: 25}},
		errors: []observability.RecentError{{RequestID: "req-safe", Endpoint: "/v1/chat/completions", HTTPStatus: 429, ErrorCode: "upstream_rate_limited", CreatedAt: "2026-07-30T03:00:00Z"}},
	}
	handler := NewStats(store, fixedAdminClock{now: time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)})

	statsRequest := httptest.NewRequest(http.MethodGet, "/admin/api/stats?days=7", nil)
	statsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statsRecorder, statsRequest)
	if statsRecorder.Code != http.StatusOK {
		t.Fatalf("stats status = %d: %s", statsRecorder.Code, statsRecorder.Body.String())
	}
	if !store.since.Equal(time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("stats since = %s", store.since)
	}
	var statsBody struct {
		Data []observability.DailyStat `json:"data"`
	}
	if err := json.NewDecoder(statsRecorder.Body).Decode(&statsBody); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if len(statsBody.Data) != 1 || statsBody.Data[0].RequestCount != 2 {
		t.Fatalf("stats body = %#v", statsBody)
	}

	errorRequest := httptest.NewRequest(http.MethodGet, "/admin/api/errors?limit=20", nil)
	errorRecorder := httptest.NewRecorder()
	handler.ServeHTTP(errorRecorder, errorRequest)
	if errorRecorder.Code != http.StatusOK {
		t.Fatalf("errors status = %d: %s", errorRecorder.Code, errorRecorder.Body.String())
	}
	if store.limit != 20 {
		t.Fatalf("error limit = %d, want 20", store.limit)
	}
	var errorsBody struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(errorRecorder.Body).Decode(&errorsBody); err != nil {
		t.Fatalf("decode errors: %v", err)
	}
	if len(errorsBody.Data) != 1 {
		t.Fatalf("errors body = %#v", errorsBody)
	}
	for _, forbidden := range []string{"message", "body", "response", "headers", "secret"} {
		if _, found := errorsBody.Data[0][forbidden]; found {
			t.Fatalf("recent error contains forbidden field %q", forbidden)
		}
	}
}

func TestStatsHandlerRejectsInvalidQueryAndMethods(t *testing.T) {
	handler := NewStats(&statsStoreStub{}, fixedAdminClock{now: time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)})
	for _, target := range []string{"/admin/api/stats?days=0", "/admin/api/stats?days=abc", "/admin/api/errors?limit=0", "/admin/api/stats/cost?days=0"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400", target, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/api/stats", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("POST stats status = %d, want 404", recorder.Code)
	}
}

func TestStatsHandlerReturnsDailyCosts(t *testing.T) {
	store := &statsStoreStub{
		costs: []observability.DailyModelCost{{
			Day: "2026-07-30", ModelID: "meta/llama-3.1-8b-instruct",
			PromptTokens: 1_000_000, CompletionTokens: 500_000,
			InputCostUSD: 0.20, OutputCostUSD: 0.20, TotalCostUSD: 0.40, Priced: true,
		}},
	}
	handler := NewStats(store, fixedAdminClock{now: time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/stats/cost?days=7", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("cost status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data []observability.DailyModelCost `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode cost: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].TotalCostUSD != 0.40 || !body.Data[0].Priced {
		t.Fatalf("cost body = %#v", body.Data)
	}
}

type statsStoreStub struct {
	stats  []observability.DailyStat
	errors []observability.RecentError
	costs  []observability.DailyModelCost
	since  time.Time
	limit  int
}

func (s *statsStoreStub) ListDailyStats(_ context.Context, since time.Time) ([]observability.DailyStat, error) {
	s.since = since
	return s.stats, nil
}

func (s *statsStoreStub) ListRecentErrors(_ context.Context, limit int) ([]observability.RecentError, error) {
	s.limit = limit
	return s.errors, nil
}

func (s *statsStoreStub) ListDailyCosts(_ context.Context, _, _ time.Time) ([]observability.DailyModelCost, error) {
	return s.costs, nil
}

type fixedAdminClock struct{ now time.Time }

func (c fixedAdminClock) Now() time.Time                            { return c.now }
func (fixedAdminClock) NewTimer(duration time.Duration) *time.Timer { return time.NewTimer(duration) }
func (fixedAdminClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}
