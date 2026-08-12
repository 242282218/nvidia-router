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
	default:
		http.NotFound(writer, request)
	}
}

// status reports the live built-in proxy-pool state (healthy/total counts and
// per-proxy quality) for the admin UI. Static-proxy mode returns zero counts.
// Collector diagnostics are exposed as safe projections only: the raw fetch
// error embeds the upstream URL (which carries credentials), so operators get
// the provider error code or a generic "transport" marker instead.
func (h *ProxyPool) status(writer http.ResponseWriter, request *http.Request) {
	status := h.service.PoolStatus()
	writeJSON(writer, http.StatusOK, map[string]any{"data": map[string]any{
		"total_size":        status.TotalSize,
		"healthy_size":      status.HealthySize,
		"proxies":           status.View(),
		"last_fetch_at":     rfc3339OrEmpty(status.LastFetchAt),
		"last_success_at":   rfc3339OrEmpty(status.LastSuccessAt),
		"last_error_code":   status.LastErrorCode,
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
		Enabled      *bool   `json:"enabled"`
		ProxyURL     *string `json:"proxy_url"`
		AuthKey      string  `json:"auth_key"`
		ClearAuthKey bool    `json:"clear_auth_key"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The proxy pool request is invalid.", err)
		return
	}
	patch := xkproxy.Patch{Enabled: input.Enabled, AuthKey: input.AuthKey, ClearAuthKey: input.ClearAuthKey}
	if input.ProxyURL != nil {
		patch.ProxyURL = *input.ProxyURL
	}
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
