package admin

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"nvidia-router/internal/providercredential"
)

// providerStore is the read/write surface the admin API needs. The concrete
// Repository satisfies it; tests substitute a stub.
type providerStore interface {
	List(context.Context) ([]providercredential.Provider, error)
	Create(context.Context, string, string, string) (providercredential.Provider, error)
	SetEnabled(context.Context, int64, bool) error
}

// ProviderCredentials exposes OpenAI-compatible provider credential management
// (e.g. SiliconFlow) behind /admin/api/providers. NVIDIA keys keep their own
// surface; this is the second-provider store.
type ProviderCredentials struct {
	store providerStore
}

func NewProviderCredentials(store providerStore) *ProviderCredentials {
	return &ProviderCredentials{store: store}
}

type providerDTO struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	BaseURL       string `json:"base_url"`
	DisplayPrefix string `json:"display_prefix"`
	DisplaySuffix string `json:"display_suffix"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func (h *ProviderCredentials) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/admin/api/providers" && request.Method == http.MethodGet:
		h.list(writer, request)
	case request.URL.Path == "/admin/api/providers" && request.Method == http.MethodPost:
		h.create(writer, request)
	case strings.HasPrefix(request.URL.Path, "/admin/api/providers/") && request.Method == http.MethodPatch:
		h.setEnabled(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *ProviderCredentials) list(writer http.ResponseWriter, request *http.Request) {
	items, err := h.store.List(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	data := make([]providerDTO, 0, len(items))
	for _, item := range items {
		data = append(data, toProviderDTO(item))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": data})
}

func (h *ProviderCredentials) create(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The provider credential request is invalid.", err)
		return
	}
	name := strings.TrimSpace(input.Name)
	baseURL := strings.TrimSpace(input.BaseURL)
	key := strings.TrimSpace(input.Key)
	if !validProviderName(name) {
		writeInvalidRequest(writer, "The provider name must be 1-32 alphanumeric characters (letters, digits, '_', '-').", nil)
		return
	}
	if baseURL == "" || key == "" {
		writeInvalidRequest(writer, "The provider base URL and key are required.", nil)
		return
	}
	if !validProviderBaseURL(baseURL) {
		writeInvalidRequest(writer, "The provider base URL must be an HTTP or HTTPS URL without credentials, query, or fragment.", nil)
		return
	}
	created, err := h.store.Create(request.Context(), name, baseURL, key)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, toProviderDTO(created))
}

func (h *ProviderCredentials) setEnabled(writer http.ResponseWriter, request *http.Request) {
	id, action, ok := parseIDRoute(request.URL.Path, "/admin/api/providers/")
	if !ok || action != "" {
		http.NotFound(writer, request)
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The provider update is invalid.", err)
		return
	}
	if input.Enabled {
		writeAdminError(writer, http.StatusBadRequest, "provider_runtime_unsupported", "This provider is not connected to the running provider runtime.", nil)
		return
	}
	if err := h.store.SetEnabled(request.Context(), id, input.Enabled); err != nil {
		if err == providercredential.ErrNotFound {
			writeAdminError(writer, http.StatusNotFound, "provider_not_found", "The provider credential was not found.", err)
			return
		}
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": id, "enabled": input.Enabled})
}

func toProviderDTO(item providercredential.Provider) providerDTO {
	return providerDTO{
		ID: item.ID, Name: item.Name, BaseURL: item.BaseURL,
		DisplayPrefix: item.DisplayPrefix, DisplaySuffix: item.DisplaySuffix,
		Enabled:   item.Enabled,
		CreatedAt: item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: item.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func validProviderName(name string) bool {
	return providerNamePattern.MatchString(name)
}

func validProviderBaseURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return false
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	if port := parsed.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return false
		}
	}
	return true
}
