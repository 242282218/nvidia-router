package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
)

func TestV1ModelsListsEnabledWhitelist(t *testing.T) {
	listed := []modelcatalog.Model{
		{ID: 1, PublicID: "public-chat", UpstreamID: "vendor/chat", DisplayName: "Chat", Kind: modelcatalog.KindChat, Enabled: true, ContextLength: 131072},
		{ID: 2, PublicID: "public-embed", UpstreamID: "vendor/embed", DisplayName: "Embed", Kind: modelcatalog.KindEmbedding, Enabled: true},
		{ID: 3, PublicID: "public-disabled", UpstreamID: "vendor/disabled", DisplayName: "Disabled", Kind: modelcatalog.KindChat, Enabled: false},
	}
	handler := NewModels(modelListerFunc(func(context.Context) ([]modelcatalog.Model, error) {
		return listed, nil
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Object string           `json:"object"`
		Data   []modelListEntry `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v; body=%s", err, response.Body.String())
	}
	if payload.Object != "list" {
		t.Fatalf("object = %q, want list", payload.Object)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("data len = %d, want 2; body=%s", len(payload.Data), response.Body.String())
	}
	for _, entry := range payload.Data {
		if entry.Object != "model" {
			t.Fatalf("entry object = %q, want model", entry.Object)
		}
		if entry.ID != "public-chat" && entry.ID != "public-embed" {
			t.Fatalf("unexpected id %q leaked disabled or upstream id", entry.ID)
		}
		if entry.Created <= 0 {
			t.Fatalf("created = %d, want positive", entry.Created)
		}
		if entry.OwnedBy == "" {
			t.Fatalf("owned_by empty for %q", entry.ID)
		}
		// Only a declared context window is exposed; undeclared (0) models omit
		// the field so clients fall back to their own defaults.
		if entry.ID == "public-chat" && (entry.ContextLength == nil || *entry.ContextLength != 131072) {
			t.Fatalf("context_length for public-chat = %v, want 131072", entry.ContextLength)
		}
		if entry.ID == "public-embed" && entry.ContextLength != nil {
			t.Fatalf("context_length for public-embed = %v, want omitted", *entry.ContextLength)
		}
	}
}

func TestV1ModelsReportsPublicErrorOnFailure(t *testing.T) {
	handler := NewModels(modelListerFunc(func(context.Context) ([]modelcatalog.Model, error) {
		return nil, modelsBackendError{message: "backend down"}
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	assertChatError(t, response, http.StatusInternalServerError, "internal_error")
}

func TestV1ModelsIgnoresDisabledModels(t *testing.T) {
	handler := NewModels(modelListerFunc(func(context.Context) ([]modelcatalog.Model, error) {
		return []modelcatalog.Model{
			{ID: 1, PublicID: "off", UpstreamID: "upstream-secret", DisplayName: "Off", Kind: modelcatalog.KindChat, Enabled: false, CapabilityVerifiedAt: ptrTime(time.Unix(100, 0))},
		}, nil
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	var payload struct {
		Data []modelListEntry `json:"data"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if len(payload.Data) != 0 {
		t.Fatalf("expected zero models for all-disabled list; got %d; body=%s", len(payload.Data), response.Body.String())
	}
	if strings.Contains(response.Body.String(), "upstream-secret") {
		t.Fatalf("upstream id leaked for disabled model: %s", response.Body.String())
	}
}

type modelListEntry struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	ContextLength *int   `json:"context_length"`
}

type modelListerFunc func(context.Context) ([]modelcatalog.Model, error)

func (f modelListerFunc) ListEnabled(ctx context.Context) ([]modelcatalog.Model, error) {
	return f(ctx)
}

type modelsBackendError struct{ message string }

func (e modelsBackendError) Error() string { return e.message }

func ptrTime(t time.Time) *time.Time { return &t }
