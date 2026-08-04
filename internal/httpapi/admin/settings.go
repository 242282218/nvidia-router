package admin

import (
	"context"
	"errors"
	"net/http"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/runtimeconfig"
)

type runtimeSettingsStore interface {
	runtimeconfig.Provider
	Store(context.Context, runtimeconfig.Snapshot) error
}

type Settings struct {
	store runtimeSettingsStore
}

type settingsDTO struct {
	QueueCapacity           int    `json:"queue_capacity"`
	QueueWaitTimeoutMS      int    `json:"queue_wait_timeout_ms"`
	ConnectTimeoutMS        int    `json:"connect_timeout_ms"`
	FirstByteTimeoutMS      int    `json:"first_byte_timeout_ms"`
	NonstreamTotalTimeoutMS int    `json:"nonstream_total_timeout_ms"`
	ShutdownGraceMS         int    `json:"shutdown_grace_ms"`
	FailoverStatusCodes     string `json:"failover_status_codes"`
	RequestLogRetentionDays int    `json:"request_log_retention_days"`
}

type settingsPatch struct {
	QueueCapacity           *int `json:"queue_capacity"`
	QueueWaitTimeoutMS      *int `json:"queue_wait_timeout_ms"`
	ConnectTimeoutMS        *int `json:"connect_timeout_ms"`
	FirstByteTimeoutMS      *int `json:"first_byte_timeout_ms"`
	NonstreamTotalTimeoutMS *int `json:"nonstream_total_timeout_ms"`
	ShutdownGraceMS         *int `json:"shutdown_grace_ms"`
	// FailoverStatusCodes is the operator-tunable failover spec (audit B4).
	// nil keeps the persisted value; an explicit empty string is the legitimate
	// "never fail over" sentinel — we let Validate pass it through and let the
	// runtime layer fall back to the documented default set.
	FailoverStatusCodes     *string `json:"failover_status_codes"`
	RequestLogRetentionDays *int    `json:"request_log_retention_days"`
}

func NewSettings(store runtimeSettingsStore) *Settings {
	return &Settings{store: store}
}

func (h *Settings) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/admin/api/settings" {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		h.get(writer)
	case http.MethodPatch:
		h.patch(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *Settings) get(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusOK, map[string]any{"data": toSettingsDTO(h.store.Snapshot())})
}

func (h *Settings) patch(writer http.ResponseWriter, request *http.Request) {
	var input settingsPatch
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The runtime settings request is invalid.", err)
		return
	}
	next := applySettingsPatch(h.store.Snapshot(), input)
	if err := runtimeconfig.Validate(next); err != nil {
		writeSettingsValidationError(writer, err)
		return
	}
	if err := h.store.Store(request.Context(), next); err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": toSettingsDTO(next)})
}

func applySettingsPatch(current runtimeconfig.Snapshot, patch settingsPatch) runtimeconfig.Snapshot {
	if patch.QueueCapacity != nil {
		current.QueueCapacity = *patch.QueueCapacity
	}
	if patch.QueueWaitTimeoutMS != nil {
		current.QueueWaitTimeoutMS = *patch.QueueWaitTimeoutMS
	}
	if patch.ConnectTimeoutMS != nil {
		current.ConnectTimeoutMS = *patch.ConnectTimeoutMS
	}
	if patch.FirstByteTimeoutMS != nil {
		current.FirstByteTimeoutMS = *patch.FirstByteTimeoutMS
	}
	if patch.NonstreamTotalTimeoutMS != nil {
		current.NonstreamTotalTimeoutMS = *patch.NonstreamTotalTimeoutMS
	}
	if patch.ShutdownGraceMS != nil {
		current.ShutdownGraceMS = *patch.ShutdownGraceMS
	}
	if patch.FailoverStatusCodes != nil {
		current.FailoverStatusCodes = *patch.FailoverStatusCodes
	}
	if patch.RequestLogRetentionDays != nil {
		current.RequestLogRetentionDays = *patch.RequestLogRetentionDays
	}
	return current
}

func toSettingsDTO(snapshot runtimeconfig.Snapshot) settingsDTO {
	return settingsDTO{
		QueueCapacity: snapshot.QueueCapacity, QueueWaitTimeoutMS: snapshot.QueueWaitTimeoutMS,
		ConnectTimeoutMS: snapshot.ConnectTimeoutMS, FirstByteTimeoutMS: snapshot.FirstByteTimeoutMS,
		NonstreamTotalTimeoutMS: snapshot.NonstreamTotalTimeoutMS, ShutdownGraceMS: snapshot.ShutdownGraceMS,
		FailoverStatusCodes:     snapshot.FailoverStatusCodes,
		RequestLogRetentionDays: snapshot.RequestLogRetentionDays,
	}
}

func writeSettingsValidationError(writer http.ResponseWriter, err error) {
	var validation *runtimeconfig.ValidationError
	if !errors.As(err, &validation) {
		writeInvalidRequest(writer, "The runtime settings request is invalid.", err)
		return
	}
	param := validation.Field
	message := "The runtime setting is outside its allowed range."
	// String-typed validators (failover_status_codes) encode the underlying
	// parse failure on Cause/StringValue rather than the integer bounds; surface
	// a more accurate message so admin UIs do not claim "outside its allowed
	// range" for a malformed spec.
	if validation.Cause != nil {
		message = "The runtime setting is invalid."
	} else if validation.StringValue != "" {
		message = "The runtime setting is invalid."
	}
	apierror.Error{
		Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_setting",
		Message: message, Param: &param, Cause: err,
	}.Write(writer)
}
