package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/observability"
)

const (
	defaultStatsDays  = 30
	maxStatsDays      = 365
	defaultErrorLimit = 50
	maxErrorLimit     = 100
)

type statsStore interface {
	ListDailyStats(context.Context, time.Time) ([]observability.DailyStat, error)
	ListRecentErrors(context.Context, int) ([]observability.RecentError, error)
	ListDailyCosts(context.Context, time.Time, time.Time) ([]observability.DailyModelCost, error)
}

type Stats struct {
	store statsStore
	clock clock.Clock
}

func NewStats(store statsStore, source clock.Clock) *Stats {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Stats{store: store, clock: source}
}

func (h *Stats) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.NotFound(writer, request)
		return
	}
	switch request.URL.Path {
	case "/admin/api/stats":
		h.daily(writer, request)
	case "/admin/api/stats/cost":
		h.cost(writer, request)
	case "/admin/api/errors":
		h.errors(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *Stats) daily(writer http.ResponseWriter, request *http.Request) {
	days, err := parseBoundedPositive(request.URL.Query().Get("days"), defaultStatsDays, maxStatsDays)
	if err != nil {
		writeInvalidRequest(writer, "The statistics range is invalid.", err)
		return
	}
	now := h.clock.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1))
	stats, err := h.store.ListDailyStats(request.Context(), since)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": stats})
}

func (h *Stats) errors(writer http.ResponseWriter, request *http.Request) {
	limit, err := parseBoundedPositive(request.URL.Query().Get("limit"), defaultErrorLimit, maxErrorLimit)
	if err != nil {
		writeInvalidRequest(writer, "The recent error limit is invalid.", err)
		return
	}
	errorsList, err := h.store.ListRecentErrors(request.Context(), limit)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": errorsList})
}

func (h *Stats) cost(writer http.ResponseWriter, request *http.Request) {
	days, err := parseBoundedPositive(request.URL.Query().Get("days"), defaultStatsDays, maxStatsDays)
	if err != nil {
		writeInvalidRequest(writer, "The statistics range is invalid.", err)
		return
	}
	now := h.clock.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1))
	costs, err := h.store.ListDailyCosts(request.Context(), since, today.AddDate(0, 0, 1))
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": costs})
}

func parseBoundedPositive(raw string, defaultValue, maximum int) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, errors.New("value is outside the allowed range")
	}
	return value, nil
}
