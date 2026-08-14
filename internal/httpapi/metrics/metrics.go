package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/xkproxy"
)

type SummaryProvider interface {
	Summary() pool.Summary
}

type requestSummaryProvider interface {
	MetricsSummary(context.Context) (observability.MetricsSummary, error)
}

// ProxyPoolProvider exposes the built-in proxy pool's live health so operators
// can alert on it via Prometheus instead of scraping the admin page. A nil
// provider (static-proxy mode) means the pool metrics are absent, which is
// itself a useful signal the operator can distinguish from "pool empty".
type ProxyPoolProvider interface {
	PoolStatus() xkproxy.PoolStatus
}

type Handler struct {
	pool      SummaryProvider
	requests  requestSummaryProvider
	proxyPool ProxyPoolProvider
}

func New(pool SummaryProvider, requests requestSummaryProvider) *Handler {
	return &Handler{pool: pool, requests: requests}
}

// WithProxyPool attaches the built-in proxy pool health to the handler. Kept as
// a separate setter so existing call sites and tests stay unchanged.
func (h *Handler) WithProxyPool(provider ProxyPoolProvider) *Handler {
	h.proxyPool = provider
	return h
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
	if h.proxyPool != nil {
		status := h.proxyPool.PoolStatus()
		writeGauge(&builder, "nvidia_router_proxy_pool_total", status.TotalSize)
		writeGauge(&builder, "nvidia_router_proxy_pool_healthy", status.HealthySize)
		panicMode := 0
		if status.PanicMode {
			panicMode = 1
		}
		writeGauge(&builder, "nvidia_router_proxy_pool_panic_mode", panicMode)
		overloaded := 0
		if status.UpstreamOverloaded {
			overloaded = 1
		}
		writeGauge(&builder, "nvidia_router_proxy_pool_upstream_overloaded", overloaded)
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
