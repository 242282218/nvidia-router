package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"nvidia-router/internal/modelhealth"
)

type modelHealthService interface {
	Summary(context.Context, string, string) (modelhealth.Summary, error)
	Settings(context.Context) (modelhealth.Settings, error)
	PatchSettings(context.Context, modelhealth.SettingsPatch) (modelhealth.Settings, error)
	RunNow()
}

type ModelHealth struct {
	service modelHealthService
}

func NewModelHealth(service modelHealthService) *ModelHealth {
	return &ModelHealth{service: service}
}

func (h *ModelHealth) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/admin/api/model-health/summary" && request.Method == http.MethodGet:
		h.summary(writer, request)
	case request.URL.Path == "/admin/api/model-health/settings" && request.Method == http.MethodGet:
		h.settings(writer, request)
	case request.URL.Path == "/admin/api/model-health/settings" && request.Method == http.MethodPatch:
		h.patchSettings(writer, request)
	case request.URL.Path == "/admin/api/model-health/run" && request.Method == http.MethodPost:
		h.run(writer)
	default:
		h.notFound(writer, request)
	}
}

func (h *ModelHealth) summary(writer http.ResponseWriter, request *http.Request) {
	rangeName := request.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = "6h"
	}
	if !validModelHealthRange(rangeName) {
		writeInvalidRequest(writer, "The model health range is invalid.", errors.New("range is invalid"))
		return
	}
	sortName := request.URL.Query().Get("sort")
	if sortName == "" {
		sortName = "availability"
	}
	if !validModelHealthSort(sortName) {
		writeInvalidRequest(writer, "The model health sort is invalid.", errors.New("sort is invalid"))
		return
	}
	if group := request.URL.Query().Get("group"); group != "" && !validModelHealthGroup(group) {
		writeInvalidRequest(writer, "The model health group is invalid.", errors.New("group is invalid"))
		return
	}
	summary, err := h.service.Summary(request.Context(), rangeName, sortName)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": summary})
}

func (h *ModelHealth) settings(writer http.ResponseWriter, request *http.Request) {
	settings, err := h.service.Settings(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": settings})
}

type modelHealthSettingsPatch struct {
	Enabled         *bool `json:"enabled"`
	IntervalSeconds *int  `json:"interval_seconds"`
	Concurrency     *int  `json:"concurrency"`
}

func (h *ModelHealth) patchSettings(writer http.ResponseWriter, request *http.Request) {
	var input modelHealthSettingsPatch
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The model health settings request is invalid.", err)
		return
	}
	updated, err := h.service.PatchSettings(request.Context(), modelhealth.SettingsPatch{
		Enabled:         input.Enabled,
		IntervalSeconds: input.IntervalSeconds,
		Concurrency:     input.Concurrency,
	})
	if err != nil {
		if errors.Is(err, modelhealth.ErrInvalidSettings) {
			writeInvalidRequest(writer, "The model health settings request is invalid.", err)
			return
		}
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": updated})
}

func (h *ModelHealth) run(writer http.ResponseWriter) {
	h.service.RunNow()
	writeJSON(writer, http.StatusAccepted, map[string]any{"data": map[string]bool{"accepted": true}})
}

func (h *ModelHealth) notFound(writer http.ResponseWriter, request *http.Request) {
	http.NotFound(writer, request)
}

func validModelHealthRange(value string) bool {
	switch value {
	case "1h", "6h", "24h", "7d":
		return true
	default:
		return false
	}
}

func validModelHealthSort(value string) bool {
	switch value {
	case "availability", "recent", "volume":
		return true
	default:
		return false
	}
}

func validModelHealthGroup(value string) bool {
	switch strings.ToLower(value) {
	case "default", "provider", "kind":
		return true
	default:
		return false
	}
}

var _ http.Handler = (*ModelHealth)(nil)
