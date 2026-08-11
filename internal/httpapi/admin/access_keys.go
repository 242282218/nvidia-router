package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/accesskey"
)

type accessKeyManager interface {
	List(context.Context) ([]accesskey.Key, error)
	Create(context.Context, string) (accesskey.CreatedKey, error)
	Revoke(context.Context, int64) error
	UpdatePolicy(context.Context, int64, *time.Time, int, int, int, *int64) error
}

type AccessKeys struct{ service accessKeyManager }

type accessKeyDTO struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Prefix         string     `json:"key_prefix"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RPMLimit       int        `json:"rpm_limit"`
	TPMLimit       int        `json:"tpm_limit"`
	MaxConcurrent  int        `json:"max_concurrent"`
	TokenBudget    int64      `json:"token_budget"`
	ConsumedTokens int64      `json:"consumed_tokens"`
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
	if !ok {
		http.NotFound(writer, request)
		return
	}
	if action == "" && request.Method == http.MethodDelete {
		if err := h.service.Revoke(request.Context(), id); err != nil {
			writeInternalError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if action == "" && request.Method == http.MethodPatch {
		h.updatePolicy(writer, request, id)
		return
	}
	http.NotFound(writer, request)
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

func (h *AccessKeys) updatePolicy(writer http.ResponseWriter, request *http.Request, id int64) {
	var input struct {
		ExpiresAt     *time.Time `json:"expires_at"`
		RPMLimit      *int       `json:"rpm_limit"`
		TPMLimit      *int       `json:"tpm_limit"`
		MaxConcurrent *int       `json:"max_concurrent"`
		TokenBudget   *int64     `json:"token_budget"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The access key policy is invalid.", err)
		return
	}
	if input.RPMLimit == nil || input.TPMLimit == nil || input.MaxConcurrent == nil {
		writeInvalidRequest(writer, "The access key policy must include all limits.", errors.New("all limits are required"))
		return
	}
	// token_budget is optional for partial updates: an omitted field keeps the
	// existing value, an explicit 0 disables the cap. Passing the pointer through
	// lets the repository COALESCE so a partial PATCH never clears the budget.
	if err := h.service.UpdatePolicy(request.Context(), id, input.ExpiresAt, *input.RPMLimit, *input.TPMLimit, *input.MaxConcurrent, input.TokenBudget); err != nil {
		if errors.Is(err, accesskey.ErrAccessKeyNotFound) {
			writeAdminError(writer, http.StatusNotFound, "access_key_not_found", "The access key was not found.", err)
			return
		}
		writeInvalidRequest(writer, "The access key policy is invalid.", err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": id, "expires_at": input.ExpiresAt, "rpm_limit": *input.RPMLimit, "tpm_limit": *input.TPMLimit, "max_concurrent": *input.MaxConcurrent, "token_budget": input.TokenBudget})
}

func toAccessKeyDTO(key accesskey.Key) accessKeyDTO {
	return accessKeyDTO{ID: key.ID, Name: key.Name, Prefix: key.Prefix, CreatedAt: key.CreatedAt, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt, ExpiresAt: key.ExpiresAt, RPMLimit: key.RPMLimit, TPMLimit: key.TPMLimit, MaxConcurrent: key.MaxConcurrent, TokenBudget: key.TokenBudget, ConsumedTokens: key.ConsumedTokens}
}
