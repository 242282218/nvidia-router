package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/observability"
)

const (
	defaultMonitoringRange    = observability.MonitoringRange24Hours
	defaultMonitoringPage     = 1
	defaultMonitoringPageSize = 50
	maxMonitoringPage         = 100000
	maxMonitoringTextLength   = 128
	// monitoringSummaryCacheTTL bounds how long a MonitoringSummary result is
	// served from memory. The admin UI refreshes the summary on a short timer and
	// the 24h window scans request_logs with five queries (aggregate, series,
	// COUNT, p50, p95) every refresh; a short TTL keeps those scans off the hot
	// reader-pool path while staying fresh enough for a dashboard.
	monitoringSummaryCacheTTL = 5 * time.Second
)

type monitoringStore interface {
	observability.MonitoringStore
}

type Monitoring struct {
	store monitoringStore
	clock clock.Clock

	summaryMu    sync.Mutex
	summaryCache monitoringSummaryCacheEntry
}

type monitoringSummaryCacheEntry struct {
	key       string
	snapshot  observability.MonitoringSnapshot
	expiresAt time.Time
}

func NewMonitoring(store monitoringStore, source clock.Clock) *Monitoring {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Monitoring{store: store, clock: source}
}

func (h *Monitoring) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.NotFound(writer, request)
		return
	}
	switch request.URL.Path {
	case "/admin/api/monitoring/summary":
		h.summary(writer, request)
	case "/admin/api/monitoring/logs":
		h.logs(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *Monitoring) summary(writer http.ResponseWriter, request *http.Request) {
	filter, err := parseMonitoringFilter(request)
	if err != nil {
		writeInvalidRequest(writer, "The monitoring filters are invalid.", err)
		return
	}
	query, err := h.query(request, filter)
	if err != nil {
		writeInvalidRequest(writer, "The monitoring query is invalid.", err)
		return
	}
	key := monitoringSummaryCacheKey(query)
	if snapshot, ok := h.cachedSummary(key); ok {
		writeJSON(writer, http.StatusOK, map[string]any{"data": snapshot})
		return
	}
	snapshot, err := h.store.MonitoringSummary(request.Context(), query)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	h.setSummaryCache(key, snapshot)
	writeJSON(writer, http.StatusOK, map[string]any{"data": snapshot})
}

// monitoringSummaryCacheKey identifies a summary query by its range and filters,
// deliberately excluding From/To: NewMonitoringQuery shifts To on every refresh,
// so a time-sensitive key would never hit. The TTL bounds the staleness instead.
func monitoringSummaryCacheKey(query observability.MonitoringQuery) string {
	var builder strings.Builder
	builder.WriteString(query.Range)
	filter := query.Filter
	builder.WriteByte('\x00')
	builder.WriteString(filter.ModelID)
	builder.WriteByte('\x00')
	builder.WriteString(filter.Endpoint)
	builder.WriteByte('\x00')
	builder.WriteString(filter.Outcome)
	builder.WriteByte('\x00')
	builder.WriteString(filter.Search)
	builder.WriteByte('\x00')
	builder.WriteString(intToString(filter.Status))
	builder.WriteByte('\x00')
	builder.WriteString(int64ToString(filter.AccessKeyID))
	builder.WriteByte('\x00')
	builder.WriteString(int64ToString(filter.NVIDIAKeyID))
	return builder.String()
}

func intToString(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func int64ToString(value *int64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatInt(*value, 10)
}

func (h *Monitoring) cachedSummary(key string) (observability.MonitoringSnapshot, bool) {
	h.summaryMu.Lock()
	defer h.summaryMu.Unlock()
	if h.summaryCache.key == key && h.clock.Now().Before(h.summaryCache.expiresAt) {
		return h.summaryCache.snapshot, true
	}
	return observability.MonitoringSnapshot{}, false
}

func (h *Monitoring) setSummaryCache(key string, snapshot observability.MonitoringSnapshot) {
	h.summaryMu.Lock()
	defer h.summaryMu.Unlock()
	h.summaryCache = monitoringSummaryCacheEntry{key: key, snapshot: snapshot, expiresAt: h.clock.Now().Add(monitoringSummaryCacheTTL)}
}

func (h *Monitoring) logs(writer http.ResponseWriter, request *http.Request) {
	filter, err := parseMonitoringFilter(request)
	if err != nil {
		writeInvalidRequest(writer, "The monitoring filters are invalid.", err)
		return
	}
	query, err := h.query(request, filter)
	if err != nil {
		writeInvalidRequest(writer, "The monitoring query is invalid.", err)
		return
	}
	page, err := parseMonitoringPage(request.URL.Query().Get("page"), defaultMonitoringPage, maxMonitoringPage)
	if err != nil {
		writeInvalidRequest(writer, "The monitoring page is invalid.", err)
		return
	}
	pageSize, err := parseMonitoringPage(request.URL.Query().Get("page_size"), defaultMonitoringPageSize, observability.MaxMonitoringPageSize)
	if err != nil {
		writeInvalidRequest(writer, "The monitoring page size is invalid.", err)
		return
	}
	logs, err := h.store.ListRequestLogs(request.Context(), observability.RequestLogsQuery{
		MonitoringQuery: query, Page: page, PageSize: pageSize,
	})
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": logs})
}

func (h *Monitoring) query(request *http.Request, filter observability.MonitoringFilter) (observability.MonitoringQuery, error) {
	rangeName := request.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = defaultMonitoringRange
	}
	return observability.NewMonitoringQuery(h.clock.Now(), rangeName, filter)
}

func parseMonitoringFilter(request *http.Request) (observability.MonitoringFilter, error) {
	values := request.URL.Query()
	modelID, err := parseMonitoringText(values.Get("model_id"))
	if err != nil {
		return observability.MonitoringFilter{}, errors.Join(errors.New("model_id is invalid"), err)
	}
	endpoint, err := parseMonitoringText(values.Get("endpoint"))
	if err != nil {
		return observability.MonitoringFilter{}, errors.Join(errors.New("endpoint is invalid"), err)
	}
	search, err := parseMonitoringText(values.Get("search"))
	if err != nil {
		return observability.MonitoringFilter{}, errors.Join(errors.New("search is invalid"), err)
	}
	outcome := strings.TrimSpace(values.Get("outcome"))
	if outcome != "" && outcome != observability.OutcomeSuccess && outcome != observability.OutcomeFailure {
		return observability.MonitoringFilter{}, errors.New("outcome is invalid")
	}
	status, err := parseOptionalMonitoringInt(values.Get("status"), 100, 599)
	if err != nil {
		return observability.MonitoringFilter{}, errors.New("status is invalid")
	}
	accessKeyID, err := parseOptionalPositiveID(values.Get("access_key_id"))
	if err != nil {
		return observability.MonitoringFilter{}, errors.New("access_key_id is invalid")
	}
	nvidiaKeyID, err := parseOptionalPositiveID(values.Get("nvidia_key_id"))
	if err != nil {
		return observability.MonitoringFilter{}, errors.New("nvidia_key_id is invalid")
	}
	return observability.MonitoringFilter{
		ModelID: modelID, Endpoint: endpoint, Outcome: outcome, Search: search,
		Status: status, AccessKeyID: accessKeyID, NVIDIAKeyID: nvidiaKeyID,
	}, nil
}

func parseMonitoringText(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > maxMonitoringTextLength {
		return "", errors.New("text value is too long")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("text value contains a control character")
	}
	return value, nil
}

func parseOptionalPositiveID(value string) (*int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return nil, errors.New("id is outside the allowed range")
	}
	return &parsed, nil
}

func parseOptionalMonitoringInt(value string, minimum, maximum int) (*int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return nil, errors.New("value is outside the allowed range")
	}
	return &parsed, nil
}

func parseMonitoringPage(raw string, defaultValue, maximum int) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, errors.New("page is outside the allowed range")
	}
	return value, nil
}

var _ http.Handler = (*Monitoring)(nil)
