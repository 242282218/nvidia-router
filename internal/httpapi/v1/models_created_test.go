package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
)

// TestV1ModelsCreatedIsStableAcrossRequests locks in the fix for a created
// timestamp built from time.Now(): the field changed on every request, so
// clients that cache or diff the model list saw the whole catalogue change
// continuously. It must now be the model's own creation time.
func TestV1ModelsCreatedIsStableAcrossRequests(t *testing.T) {
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	handler := NewModels(modelListerFunc(func(context.Context) ([]modelcatalog.Model, error) {
		return []modelcatalog.Model{
			{PublicID: "chat-a", Kind: modelcatalog.KindChat, Enabled: true, CreatedAt: created},
		}, nil
	}))

	first := listModelsCreated(t, handler)
	time.Sleep(2 * time.Millisecond)
	second := listModelsCreated(t, handler)

	if first["chat-a"] != created.Unix() {
		t.Fatalf("created = %d, want %d (model creation time)", first["chat-a"], created.Unix())
	}
	if first["chat-a"] != second["chat-a"] {
		t.Fatalf("created changed across requests: %d then %d", first["chat-a"], second["chat-a"])
	}
}

// TestV1ModelsCreatedIsPerModel verifies each model reports its own creation
// time rather than one shared value computed per response.
func TestV1ModelsCreatedIsPerModel(t *testing.T) {
	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	handler := NewModels(modelListerFunc(func(context.Context) ([]modelcatalog.Model, error) {
		return []modelcatalog.Model{
			{PublicID: "old", Kind: modelcatalog.KindChat, Enabled: true, CreatedAt: older},
			{PublicID: "new", Kind: modelcatalog.KindChat, Enabled: true, CreatedAt: newer},
		}, nil
	}))

	got := listModelsCreated(t, handler)
	if got["old"] != older.Unix() || got["new"] != newer.Unix() {
		t.Fatalf("created values = %#v, want old=%d new=%d", got, older.Unix(), newer.Unix())
	}
}

func listModelsCreated(t *testing.T, handler http.Handler) map[string]int64 {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode models response: %v", err)
	}
	out := make(map[string]int64, len(payload.Data))
	for _, item := range payload.Data {
		out[item.ID] = item.Created
	}
	return out
}
