package admin

import (
	"net/http"

	"nvidia-router/internal/pool"
)

type runtimeSummaryProvider interface {
	Summary() pool.Summary
}

type Runtime struct {
	provider runtimeSummaryProvider
}

func NewRuntime(provider runtimeSummaryProvider) *Runtime {
	return &Runtime{provider: provider}
}

func (h *Runtime) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/admin/api/runtime/summary" || request.Method != http.MethodGet {
		http.NotFound(writer, request)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": h.provider.Summary()})
}
