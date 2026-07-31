package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nvidia-router/internal/modelcatalog"
)

type modelManager interface {
	DiscoverCandidates(context.Context, int64) ([]modelcatalog.Candidate, error)
	SaveSelection(context.Context, []modelcatalog.Selection) error
	List(context.Context) ([]modelcatalog.Model, error)
	Patch(context.Context, int64, modelcatalog.Patch) (modelcatalog.Model, error)
	VerifyAndUnblock(context.Context, int64, int64) (modelcatalog.Model, error)
}

type modelVerificationDTO struct {
	KeyID int64 `json:"key_id"`
}

type candidateKeySource interface {
	FirstEnabledID(context.Context) (int64, error)
}

type modelStateSync interface {
	SetModelEnabled(int64, bool)
	ClearModelBlocks(int64)
}

type Models struct {
	service modelManager
	keys    candidateKeySource
	sync    StateSync
}

type candidateDTO struct {
	UpstreamID          string            `json:"upstream_id"`
	DisplayName         string            `json:"display_name"`
	Kind                modelcatalog.Kind `json:"kind"`
	SupportsVision      bool              `json:"supports_vision"`
	SupportsTools       bool              `json:"supports_tools"`
	SupportsReasoning   bool              `json:"supports_reasoning"`
	ReasoningWireFormat string            `json:"reasoning_wire_format"`
}
type modelDTO struct {
	ID                   int64             `json:"id"`
	PublicID             string            `json:"public_id"`
	UpstreamID           string            `json:"upstream_id"`
	DisplayName          string            `json:"display_name"`
	Kind                 modelcatalog.Kind `json:"kind"`
	Enabled              bool              `json:"enabled"`
	SupportsVision       bool              `json:"supports_vision"`
	SupportsTools        bool              `json:"supports_tools"`
	SupportsReasoning    bool              `json:"supports_reasoning"`
	ReasoningWireFormat  string            `json:"reasoning_wire_format"`
	CapabilityVerifiedAt *time.Time        `json:"capability_verified_at,omitempty"`
	BlockedByKeyIDs      []int64           `json:"blocked_by_key_ids"`
}
type selectionDTO struct {
	PublicID            string            `json:"public_id"`
	UpstreamID          string            `json:"upstream_id"`
	DisplayName         string            `json:"display_name"`
	Kind                modelcatalog.Kind `json:"kind"`
	Enabled             bool              `json:"enabled"`
	SupportsVision      bool              `json:"supports_vision"`
	SupportsTools       bool              `json:"supports_tools"`
	SupportsReasoning   bool              `json:"supports_reasoning"`
	ReasoningWireFormat string            `json:"reasoning_wire_format"`
}

func NewModels(service modelManager, keys candidateKeySource, syncer StateSync) *Models {
	return &Models{service: service, keys: keys, sync: syncer}
}

func (h *Models) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/admin/api/models/candidates" && request.Method == http.MethodGet:
		h.candidates(writer, request)
	case request.URL.Path == "/admin/api/models" && request.Method == http.MethodGet:
		h.list(writer, request)
	case request.URL.Path == "/admin/api/models" && request.Method == http.MethodPost:
		h.save(writer, request)
	case strings.HasPrefix(request.URL.Path, "/admin/api/key-model-blocks/") && request.Method == http.MethodDelete:
		h.unblock(writer, request)
	case strings.HasPrefix(request.URL.Path, "/admin/api/models/") && request.Method == http.MethodPatch:
		h.patch(writer, request)
	case strings.HasPrefix(request.URL.Path, "/admin/api/models/") && request.Method == http.MethodPost:
		h.verify(writer, request)

	default:
		http.NotFound(writer, request)
	}
}

func (h *Models) candidates(writer http.ResponseWriter, request *http.Request) {
	keyID, err := h.keys.FirstEnabledID(request.Context())
	if err != nil {
		writeAdminError(writer, http.StatusServiceUnavailable, "no_available_keys", "No NVIDIA key is available for model discovery.", err)
		return
	}
	items, err := h.service.DiscoverCandidates(request.Context(), keyID)
	if err != nil {
		writeAdminError(writer, http.StatusBadGateway, "upstream_error", "The NVIDIA model list could not be loaded.", err)
		return
	}
	data := make([]candidateDTO, 0, len(items))
	for _, item := range items {
		data = append(data, toCandidateDTO(item))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}
func (h *Models) list(writer http.ResponseWriter, request *http.Request) {
	items, err := h.service.List(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	data := make([]modelDTO, 0, len(items))
	for _, item := range items {
		data = append(data, toModelDTO(item))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}
func (h *Models) save(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Models []selectionDTO `json:"models"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The model selection request is invalid.", err)
		return
	}
	selections := make([]modelcatalog.Selection, 0, len(input.Models))
	for _, item := range input.Models {
		selections = append(selections, item.selection())
	}
	before, _ := h.service.List(request.Context())
	previousKinds := make(map[string]modelcatalog.Kind, len(before))
	for _, model := range before {
		previousKinds[model.PublicID] = model.Kind
	}
	if err := h.service.SaveSelection(request.Context(), selections); err != nil {
		h.writeModelError(writer, err)
		return
	}
	if syncer, ok := h.sync.(modelStateSync); ok {
		models, err := h.service.List(request.Context())
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		for _, selection := range selections {
			for _, model := range models {
				if model.PublicID != selection.PublicID {
					continue
				}
				syncer.SetModelEnabled(model.ID, model.Enabled)
				if previous, exists := previousKinds[model.PublicID]; exists && previous != model.Kind {
					syncer.ClearModelBlocks(model.ID)
				}
			}
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"saved": len(selections)})
}
func (h *Models) verify(writer http.ResponseWriter, request *http.Request) {
	id, action, ok := parseIDRoute(request.URL.Path, "/admin/api/models/")
	if !ok || (action != "verify" && action != "test") {
		http.NotFound(writer, request)
		return
	}
	var input modelVerificationDTO
	if err := decodeJSON(writer, request, &input); err != nil || input.KeyID <= 0 {
		if err == nil {
			err = errors.New("key_id must be a positive integer")
		}
		writeInvalidRequest(writer, "The model verification request is invalid.", err)
		return
	}
	model, err := h.service.VerifyAndUnblock(request.Context(), input.KeyID, id)
	if err != nil {
		h.writeModelError(writer, err)
		return
	}
	if h.sync != nil {
		h.sync.SetModelBlock(input.KeyID, id, false)
	}
	writeJSON(writer, http.StatusOK, toModelDTO(model))
}

func (h *Models) patch(writer http.ResponseWriter, request *http.Request) {
	id, action, ok := parseIDRoute(request.URL.Path, "/admin/api/models/")
	if !ok || action != "" {
		http.NotFound(writer, request)
		return
	}
	var input modelcatalog.Patch
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The model update request is invalid.", err)
		return
	}
	previousKind := modelcatalog.Kind("")
	if models, listErr := h.service.List(request.Context()); listErr == nil {
		for _, item := range models {
			if item.ID == id {
				previousKind = item.Kind
				break
			}
		}
	}
	model, err := h.service.Patch(request.Context(), id, input)
	if err != nil {
		h.writeModelError(writer, err)
		return
	}
	if syncer, ok := h.sync.(modelStateSync); ok {
		syncer.SetModelEnabled(model.ID, model.Enabled)
		if previousKind != "" && previousKind != model.Kind {
			syncer.ClearModelBlocks(model.ID)
		}
	}
	writeJSON(writer, http.StatusOK, toModelDTO(model))
}
func (h *Models) unblock(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/admin/api/key-model-blocks/"), "/")
	if len(parts) != 2 {
		http.NotFound(writer, request)
		return
	}
	keyID, e1 := strconv.ParseInt(parts[0], 10, 64)
	modelID, e2 := strconv.ParseInt(parts[1], 10, 64)
	if e1 != nil || e2 != nil || keyID <= 0 || modelID <= 0 {
		http.NotFound(writer, request)
		return
	}
	model, err := h.service.VerifyAndUnblock(request.Context(), keyID, modelID)
	if err != nil {
		h.writeModelError(writer, err)
		return
	}
	h.sync.SetModelBlock(keyID, modelID, false)
	writeJSON(writer, http.StatusOK, toModelDTO(model))
}
func (h *Models) writeModelError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, modelcatalog.ErrCapabilityUnverified):
		writeAdminError(writer, http.StatusBadRequest, "capability_unverified", "The model capability must be verified before it can be enabled.", err)
	case errors.Is(err, modelcatalog.ErrManualTestRequired):
		writeAdminError(writer, http.StatusBadRequest, "manual_test_required", "A successful manual model test is required.", err)
	case errors.Is(err, modelcatalog.ErrModelNotFound):
		writeAdminError(writer, http.StatusNotFound, "model_not_found", "The model was not found.", err)
	default:
		writeInvalidRequest(writer, "The model request is invalid.", err)
	}
}
func toCandidateDTO(v modelcatalog.Candidate) candidateDTO {
	return candidateDTO{v.UpstreamID, v.DisplayName, v.Kind, v.SupportsVision, v.SupportsTools, v.SupportsReasoning, v.ReasoningWireFormat}
}
func toModelDTO(v modelcatalog.Model) modelDTO {
	return modelDTO{
		ID:                   v.ID,
		PublicID:             v.PublicID,
		UpstreamID:           v.UpstreamID,
		DisplayName:          v.DisplayName,
		Kind:                 v.Kind,
		Enabled:              v.Enabled,
		SupportsVision:       v.SupportsVision,
		SupportsTools:        v.SupportsTools,
		SupportsReasoning:    v.SupportsReasoning,
		ReasoningWireFormat:  v.ReasoningWireFormat,
		CapabilityVerifiedAt: v.CapabilityVerifiedAt,
		BlockedByKeyIDs:      v.BlockedByKeyIDs,
	}
}
func (v selectionDTO) selection() modelcatalog.Selection {
	return modelcatalog.Selection{PublicID: v.PublicID, UpstreamID: v.UpstreamID, DisplayName: v.DisplayName, Kind: v.Kind, Enabled: v.Enabled, SupportsVision: v.SupportsVision, SupportsTools: v.SupportsTools, SupportsReasoning: v.SupportsReasoning, ReasoningWireFormat: v.ReasoningWireFormat}
}
