package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
)

type SummaryProvider interface {
	Summary() pool.Summary
}

type requestSummaryProvider interface {
	MetricsSummary(context.Context) (observability.MetricsSummary, error)
}

type Handler struct {
	pool     SummaryProvider
	requests requestSummaryProvider
}

func New(pool SummaryProvider, requests requestSummaryProvider) *Handler {
	return &Handler{pool: pool, requests: requests}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var builder strings.Builder
	builder.WriteString("# HELP nvidia_router_up Whether the router process is serving metrics.\n")
	builder.WriteString("# TYPE nvidia_router_up gauge\n")
	builder.WriteString("nvidia_router_up 1\n")

	if h.pool != nil {
		summary := h.pool.Summary()
		writeGauge(&builder, "nvidia_router_keys_total", summary.Keys.Total)
		writeGauge(&builder, "nvidia_router_keys_enabled", summary.Keys.Enabled)
		writeGauge(&builder, "nvidia_router_keys_cooling_down", summary.Keys.CoolingDown)
		writeGauge(&builder, "nvidia_router_keys_ready", summary.Keys.Ready)
		writeGauge(&builder, "nvidia_router_active_leases", summary.Active)
		writeGauge(&builder, "nvidia_router_queue_length", summary.Queue.Length)
		writeGauge(&builder, "nvidia_router_queue_capacity", summary.Queue.Capacity)
	}
	if h.requests != nil {
		if summary, err := h.requests.MetricsSummary(request.Context()); err == nil {
			writeCounter(&builder, "nvidia_router_requests_total", summary.Requests)
			writeCounter(&builder, "nvidia_router_requests_succeeded_total", summary.Successes)
			writeCounter(&builder, "nvidia_router_requests_failed_total", summary.Failures)
		}
	}

	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(builder.String()))
}

func writeGauge(builder *strings.Builder, name string, value int) {
	fmt.Fprintf(builder, "# TYPE %s gauge\n%s %d\n", name, name, value)
}

func writeCounter(builder *strings.Builder, name string, value int64) {
	fmt.Fprintf(builder, "# TYPE %s counter\n%s %s\n", name, name, strconv.FormatInt(value, 10))
}
