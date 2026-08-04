package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
)

type ModelLister interface {
	ListEnabled(ctx context.Context) ([]modelcatalog.Model, error)
}

type Models struct {
	models ModelLister
}

func NewModels(models ModelLister) *Models {
	return &Models{models: models}
}

type modelsResponse struct {
	Object string     `json:"object"`
	Data   []modelDTO `json:"data"`
}

type modelDTO struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (h *Models) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		apierror.Error{
			Status:  http.StatusMethodNotAllowed,
			Type:    "invalid_request_error",
			Code:    "method_not_allowed",
			Message: "Only GET is supported for this endpoint.",
		}.Write(writer)
		return
	}
	models, err := h.models.ListEnabled(request.Context())
	if err != nil {
		writeModelsError(writer, err)
		return
	}

	dto := make([]modelDTO, 0, len(models))
	for _, model := range models {
		if !model.Enabled {
			continue
		}
		dto = append(dto, modelDTO{
			ID:     model.PublicID,
			Object: "model",
			// Created must be stable per model. It previously used time.Now(),
			// so every listing returned different values for the same models
			// and clients could not diff or cache the catalog.
			Created: modelCreatedUnix(model.CreatedAt),
			OwnedBy: "nvidia",
		})
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(modelsResponse{Object: "list", Data: dto})
}

func writeModelsError(writer http.ResponseWriter, err error) {
	var publicError *apierror.Error
	if errors.As(err, &publicError) {
		publicError.Write(writer)
		return
	}
	apierror.Error{
		Status:  http.StatusInternalServerError,
		Type:    "server_error",
		Code:    "internal_error",
		Message: "The server could not list models.",
		Cause:   err,
	}.Write(writer)
}

// processStart is the fallback creation time for a model row whose created_at is
// zero. The column is NOT NULL so this only happens for synthetic values, but
// emitting a zero time.Time would put a negative epoch on the wire, which is
// worse than the unstable timestamp this replaced. A process-scoped constant
// keeps the field stable across requests.
var processStart = time.Now().Unix()

func modelCreatedUnix(createdAt time.Time) int64 {
	if createdAt.IsZero() {
		return processStart
	}
	return createdAt.Unix()
}
