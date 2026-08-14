package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"nvidia-router/internal/xkproxy"
)

type proxyPoolService interface {
	Snapshot(context.Context) (xkproxy.Snapshot, error)
	Update(context.Context, xkproxy.Patch) (xkproxy.Snapshot, error)
	PoolStatus() xkproxy.PoolStatus
	Refresh(context.Context) error
}

type ProxyPool struct {
	service proxyPoolService
}

func NewProxyPool(service proxyPoolService) *ProxyPool {
	return &ProxyPool{service: service}
}

func (h *ProxyPool) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/admin/api/proxy-pool" && request.Method == http.MethodGet:
		h.get(writer, request)
	case request.URL.Path == "/admin/api/proxy-pool" && request.Method == http.MethodPatch:
		h.patch(writer, request)
	case request.URL.Path == "/admin/api/proxy-pool/status" && request.Method == http.MethodGet:
		h.status(writer, request)
	case request.URL.Path == "/admin/api/proxy-pool/refresh" && request.Method == http.MethodPost:
		h.refresh(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *ProxyPool) refresh(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.Refresh(request.Context()); err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"error": map[string]string{
			"code": "proxy_refresh_failed", "message": "代理池立即采集失败，请检查上游状态。",
		}})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": map[string]string{"message": "代理池已完成一轮采集。"}})
}

// status reports the live built-in proxy-pool state (healthy/total counts and
// per-proxy quality) for the admin UI. Static-proxy mode returns zero counts.
// Collector diagnostics are exposed as safe projections only: the raw fetch
// error embeds the upstream URL (which carries credentials), so operators get
// the provider error code or a generic "transport" marker instead.
func (h *ProxyPool) status(writer http.ResponseWriter, request *http.Request) {
	status := h.service.PoolStatus()
	proxies := status.View()
	if proxies == nil {
		proxies = []xkproxy.ProxyStatus{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": map[string]any{
		"configured":                status.Configured,
		"mode":                      status.Mode,
		"endpoint":                  status.Endpoint,
		"reachable":                 status.Reachable,
		"health_latency_ms":         status.HealthLatencyMS,
		"total_size":                status.TotalSize,
		"healthy_size":              status.HealthySize,
		"collector_enabled":         status.CollectorEnabled,
		"panic_mode":                status.PanicMode,
		"upstream_overloaded":       status.UpstreamOverloaded,
		"last_upstream_overload_at": rfc3339OrEmpty(status.LastUpstreamOverloadAt),
		"proxies":                   proxies,
		"last_fetch_at":             rfc3339OrEmpty(status.LastFetchAt),
		"last_success_at":           rfc3339OrEmpty(status.LastSuccessAt),
		"last_error_code":           status.LastErrorCode,
	}})
}

func rfc3339OrEmpty(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (h *ProxyPool) get(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := h.service.Snapshot(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": snapshot})
}

func (h *ProxyPool) patch(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled          *bool   `json:"enabled"`
		ProxyURL         string  `json:"proxy_url"`
		AuthKey          string  `json:"auth_key"`
		ClearAuthKey     bool    `json:"clear_auth_key"`
		UpstreamURL      *string `json:"upstream_url"`
		ValidationURL    *string `json:"validation_url"`
		ValidationStatus *int    `json:"validation_status"`
		Interval         *string `json:"interval"`
		ProxyTTL         *string `json:"proxy_ttl"`
		ExpectedQty      *int    `json:"expected_qty"`
		Concurrency      *int    `json:"concurrency"`
		MaxLatency       *string `json:"max_latency"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The proxy pool request is invalid.", err)
		return
	}
	patch := xkproxy.Patch{Enabled: input.Enabled, ProxyURL: input.ProxyURL, AuthKey: input.AuthKey, ClearAuthKey: input.ClearAuthKey, UpstreamURL: input.UpstreamURL, ValidationURL: input.ValidationURL, ValidationStatus: input.ValidationStatus, Interval: input.Interval, ProxyTTL: input.ProxyTTL, ExpectedQty: input.ExpectedQty, Concurrency: input.Concurrency, MaxLatency: input.MaxLatency}
	snapshot, err := h.service.Update(request.Context(), patch)
	if err != nil {
		var validation *xkproxy.ValidationError
		if errors.As(err, &validation) {
			writeAdminError(writer, http.StatusBadRequest, "invalid_proxy_pool", validation.Error(), err)
			return
		}
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": snapshot})
}
