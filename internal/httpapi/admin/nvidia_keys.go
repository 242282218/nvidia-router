package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/nvidiakey"
)

type StateSync interface {
	UpsertKey(keystate.KeySnapshot)
	SetKeyEnabled(int64, bool)
	RemoveKey(int64)
	SetModelBlock(int64, int64, bool)
}

type nvidiaKeyManager interface {
	List(context.Context) ([]nvidiakey.Key, error)
	Import(context.Context, string) (nvidiakey.ImportResult, error)
	ImportBatch(context.Context, string) []nvidiakey.ImportResult
	SetEnabled(context.Context, int64, bool) (keystate.KeySnapshot, error)
	Delete(context.Context, int64) error
	Test(context.Context, int64) (nvidiakey.TestResult, error)
}

type NVIDIAKeys struct {
	service nvidiaKeyManager
	sync    StateSync
}

type nvidiaKeyDTO struct {
	ID                  int64      `json:"id"`
	Masked              string     `json:"masked"`
	Enabled             bool       `json:"enabled"`
	AuthInvalid         bool       `json:"auth_invalid"`
	CooldownUntil       *time.Time `json:"cooldown_until,omitempty"`
	CooldownReason      string     `json:"cooldown_reason,omitempty"`
	CooldownLevel       int        `json:"cooldown_level"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt         *time.Time `json:"last_error_at,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type importResultDTO struct {
	Line   int           `json:"line,omitempty"`
	Status string        `json:"status"`
	Reason string        `json:"reason,omitempty"`
	Masked string        `json:"masked"`
	Key    *nvidiaKeyDTO `json:"key,omitempty"`
}

func NewNVIDIAKeys(service nvidiaKeyManager, syncer StateSync) *NVIDIAKeys {
	return &NVIDIAKeys{service: service, sync: syncer}
}

func (h *NVIDIAKeys) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/admin/api/nvidia-keys" && request.Method == http.MethodGet:
		h.list(writer, request)
	case request.URL.Path == "/admin/api/nvidia-keys" && request.Method == http.MethodPost:
		h.importOne(writer, request)
	case request.URL.Path == "/admin/api/nvidia-keys/batch" && request.Method == http.MethodPost:
		h.importBatch(writer, request)
	case request.URL.Path == "/admin/api/nvidia-keys/test-all" && request.Method == http.MethodPost:
		h.testAll(writer, request)
	default:
		h.keyRoute(writer, request)
	}
}

func (h *NVIDIAKeys) list(writer http.ResponseWriter, request *http.Request) {
	keys, err := h.service.List(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	data := make([]nvidiaKeyDTO, 0, len(keys))
	for _, key := range keys {
		data = append(data, toNVIDIAKeyDTO(key))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}

func (h *NVIDIAKeys) importOne(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The NVIDIA key request is invalid.", err)
		return
	}
	result, err := h.service.Import(request.Context(), input.Key)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if result.Key != nil {
		h.sync.UpsertKey(snapshotFromKey(*result.Key))
	}
	writeJSON(writer, http.StatusCreated, toImportResultDTO(result))
}

func (h *NVIDIAKeys) importBatch(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Keys string `json:"keys"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The NVIDIA key batch request is invalid.", err)
		return
	}
	results := h.service.ImportBatch(request.Context(), input.Keys)
	data := make([]importResultDTO, 0, len(results))
	for _, result := range results {
		if result.Key != nil {
			h.sync.UpsertKey(snapshotFromKey(*result.Key))
		}
		data = append(data, toImportResultDTO(result))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}

func (h *NVIDIAKeys) testAll(writer http.ResponseWriter, request *http.Request) {
	keys, err := h.service.List(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	data := make([]nvidiakey.TestResult, 0, len(keys))
	for _, key := range keys {
		result, err := h.service.Test(request.Context(), key.ID)
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		if result.Snapshot.ID != 0 {
			h.sync.UpsertKey(result.Snapshot)
		}
		data = append(data, result)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}

func (h *NVIDIAKeys) keyRoute(writer http.ResponseWriter, request *http.Request) {
	id, action, ok := parseIDRoute(request.URL.Path, "/admin/api/nvidia-keys/")
	if !ok {
		http.NotFound(writer, request)
		return
	}
	if action == "test" && request.Method == http.MethodPost {
		result, err := h.service.Test(request.Context(), id)
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		if result.Snapshot.ID != 0 {
			h.sync.UpsertKey(result.Snapshot)
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if action != "" {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodPatch:
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeJSON(writer, request, &input); err != nil || input.Enabled == nil {
			writeInvalidRequest(writer, "The NVIDIA key update is invalid.", err)
			return
		}
		snapshot, err := h.service.SetEnabled(request.Context(), id, *input.Enabled)
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		h.sync.SetKeyEnabled(id, snapshot.Enabled)
		writeJSON(writer, http.StatusOK, map[string]any{"id": id, "enabled": snapshot.Enabled})
	case http.MethodDelete:
		if err := h.service.Delete(request.Context(), id); err != nil {
			writeInternalError(writer, err)
			return
		}
		h.sync.RemoveKey(id)
		writer.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(writer, request)
	}
}

func toNVIDIAKeyDTO(key nvidiakey.Key) nvidiaKeyDTO {
	return nvidiaKeyDTO{ID: key.ID, Masked: key.DisplayPrefix + "…" + key.DisplaySuffix, Enabled: key.Enabled,
		AuthInvalid: key.AuthInvalid, CooldownUntil: key.CooldownUntil, CooldownReason: key.CooldownReason,
		CooldownLevel: key.CooldownLevel, ConsecutiveFailures: key.ConsecutiveFailures,
		LastSuccessAt: key.LastSuccessAt, LastErrorAt: key.LastErrorAt, LastErrorCode: key.LastErrorCode,
		CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt}
}

func toImportResultDTO(result nvidiakey.ImportResult) importResultDTO {
	dto := importResultDTO{Line: result.Line, Status: string(result.Status), Reason: result.Reason, Masked: result.Masked}
	if result.Key != nil {
		key := toNVIDIAKeyDTO(*result.Key)
		dto.Key = &key
	}
	return dto
}

func snapshotFromKey(key nvidiakey.Key) keystate.KeySnapshot {
	return keystate.KeySnapshot{ID: key.ID, Enabled: key.Enabled, AuthInvalid: key.AuthInvalid, CooldownUntil: key.CooldownUntil, CooldownLevel: key.CooldownLevel, ConsecutiveFailures: key.ConsecutiveFailures}
}

func parseIDRoute(path, prefix string) (int64, string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 0 || len(parts) > 2 || parts[0] == "" {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	return id, action, true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAdminError(writer http.ResponseWriter, status int, code, message string, cause error) {
	apierror.Error{Status: status, Type: "invalid_request_error", Code: code, Message: message, Cause: cause}.Write(writer)
}
