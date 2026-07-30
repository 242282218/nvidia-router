package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/accesskey"
)

type accessKeyManager interface {
	List(context.Context) ([]accesskey.Key, error)
	Create(context.Context, string) (accesskey.CreatedKey, error)
	Revoke(context.Context, int64) error
}

type AccessKeys struct{ service accessKeyManager }

type accessKeyDTO struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"key_prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type createdAccessKeyDTO struct {
	accessKeyDTO
	Key string `json:"key"`
}

func NewAccessKeys(service accessKeyManager) *AccessKeys { return &AccessKeys{service: service} }

func (h *AccessKeys) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/admin/api/access-keys" {
		switch request.Method {
		case http.MethodGet:
			h.list(writer, request)
		case http.MethodPost:
			h.create(writer, request)
		default:
			http.NotFound(writer, request)
		}
		return
	}
	id, action, ok := parseIDRoute(request.URL.Path, "/admin/api/access-keys/")
	if !ok || action != "" || request.Method != http.MethodDelete {
		http.NotFound(writer, request)
		return
	}
	if err := h.service.Revoke(request.Context(), id); err != nil {
		writeInternalError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *AccessKeys) list(writer http.ResponseWriter, request *http.Request) {
	keys, err := h.service.List(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	data := make([]accessKeyDTO, 0, len(keys))
	for _, key := range keys {
		data = append(data, toAccessKeyDTO(key))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}

func (h *AccessKeys) create(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(writer, request, &input); err != nil || strings.TrimSpace(input.Name) == "" {
		writeInvalidRequest(writer, "The access key request is invalid.", err)
		return
	}
	created, err := h.service.Create(request.Context(), strings.TrimSpace(input.Name))
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, createdAccessKeyDTO{accessKeyDTO: toAccessKeyDTO(created.Key), Key: created.Plaintext})
}

func toAccessKeyDTO(key accesskey.Key) accessKeyDTO {
	return accessKeyDTO{ID: key.ID, Name: key.Name, Prefix: key.Prefix, CreatedAt: key.CreatedAt, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt}
}
