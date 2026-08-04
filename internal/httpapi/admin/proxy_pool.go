package admin

import (
	"context"
	"errors"
	"net/http"

	"nvidia-router/internal/xkproxy"
)

type proxyPoolService interface {
	Snapshot(context.Context) (xkproxy.Snapshot, error)
	Update(context.Context, xkproxy.Patch) (xkproxy.Snapshot, error)
}

type ProxyPool struct {
	service proxyPoolService
}

func NewProxyPool(service proxyPoolService) *ProxyPool {
	return &ProxyPool{service: service}
}

func (h *ProxyPool) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/admin/api/proxy-pool" {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		h.get(writer, request)
	case http.MethodPatch:
		h.patch(writer, request)
	default:
		http.NotFound(writer, request)
	}
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
