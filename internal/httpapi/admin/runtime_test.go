package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/pool"
)

type fakeRuntimeSummary struct {
	summary pool.Summary
}

func (f fakeRuntimeSummary) Summary() pool.Summary { return f.summary }

func TestRuntimeSummaryReturnsOnlyOperationalMetadata(t *testing.T) {
	cooldown := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	handler := NewRuntime(fakeRuntimeSummary{summary: pool.Summary{
		Keys:             pool.KeyStatusCounts{Total: 3, Enabled: 2, Disabled: 1, AuthInvalid: 1, CoolingDown: 1},
		Active:           2,
		Queue:            pool.QueueSummary{Length: 4, Capacity: 9},
		EarliestCooldown: &cooldown,
		ShuttingDown:     true,
	}})
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/runtime/summary", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"secret", "ciphertext", "nonce", "authorization", "request_body"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, body)
		}
	}
	for _, expected := range []string{"\"active\":2", "\"length\":4", "\"capacity\":9", "\"shutting_down\":true", "2026-07-30T10:00:00Z"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("summary missing %q: %s", expected, body)
		}
	}
	var payload struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	assertJSONFields(t, payload.Data, "keys", "active", "queue", "earliest_cooldown", "shutting_down")
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(payload.Data["keys"], &keys); err != nil {
		t.Fatalf("decode key counts: %v", err)
	}
	assertJSONFields(t, keys, "total", "enabled", "disabled", "auth_invalid", "cooling_down", "ready")
	var queue map[string]json.RawMessage
	if err := json.Unmarshal(payload.Data["queue"], &queue); err != nil {
		t.Fatalf("decode queue summary: %v", err)
	}
	assertJSONFields(t, queue, "length", "capacity")
}

func TestRuntimeSummaryRejectsNonGETRoutes(t *testing.T) {
	handler := NewRuntime(fakeRuntimeSummary{})
	response := performAdminRequest(handler, http.MethodPatch, "/admin/api/runtime/summary", "{}")
	if response.Code != http.StatusNotFound {
		t.Fatalf("PATCH status = %d, want 404", response.Code)
	}
}

func assertJSONFields(t *testing.T, fields map[string]json.RawMessage, expected ...string) {
	t.Helper()
	if len(fields) != len(expected) {
		t.Fatalf("JSON fields = %v, want %v", fields, expected)
	}
	for _, name := range expected {
		if _, ok := fields[name]; !ok {
			t.Fatalf("JSON field %q is missing: %v", name, fields)
		}
	}
}
