package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/runtimeconfig"
)

type fakeRuntimeSettingsStore struct {
	snapshot runtimeconfig.Snapshot
	stored   []runtimeconfig.Snapshot
	err      error
}

func (f *fakeRuntimeSettingsStore) Snapshot() runtimeconfig.Snapshot { return f.snapshot }
func (f *fakeRuntimeSettingsStore) Store(_ context.Context, next runtimeconfig.Snapshot) error {
	if f.err != nil {
		return f.err
	}
	f.snapshot = next
	f.stored = append(f.stored, next)
	return nil
}

func TestSettingsGETReturnsCurrentRuntimeConfiguration(t *testing.T) {
	store := &fakeRuntimeSettingsStore{snapshot: runtimeconfig.Snapshot{
		QueueCapacity: 7, QueueWaitTimeoutMS: 2000, ConnectTimeoutMS: 3000,
		FirstByteTimeoutMS: 4000, NonstreamTotalTimeoutMS: 5000, ShutdownGraceMS: 6000,
	}}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodGet, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data settingsDTO `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if body.Data != toSettingsDTO(store.snapshot) {
		t.Fatalf("GET data = %#v, want %#v", body.Data, store.snapshot)
	}
}

func TestSettingsPATCHRejectsValuesOutsideDatabaseChecks(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "queue capacity below minimum", body: `{"queue_capacity":0}`},
		{name: "queue capacity above maximum", body: `{"queue_capacity":10001}`},
		{name: "queue wait below minimum", body: `{"queue_wait_timeout_ms":999}`},
		{name: "queue wait above maximum", body: `{"queue_wait_timeout_ms":600001}`},
		{name: "connect below minimum", body: `{"connect_timeout_ms":999}`},
		{name: "connect above maximum", body: `{"connect_timeout_ms":120001}`},
		{name: "first byte below minimum", body: `{"first_byte_timeout_ms":999}`},
		{name: "first byte above maximum", body: `{"first_byte_timeout_ms":600001}`},
		{name: "total below minimum", body: `{"nonstream_total_timeout_ms":999}`},
		{name: "total above maximum", body: `{"nonstream_total_timeout_ms":1800001}`},
		{name: "shutdown below minimum", body: `{"shutdown_grace_ms":999}`},
		{name: "shutdown above maximum", body: `{"shutdown_grace_ms":600001}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeRuntimeSettingsStore{snapshot: current}
			response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("PATCH status = %d, want 400: %s", response.Code, response.Body.String())
			}
			if len(store.stored) != 0 || store.snapshot != current {
				t.Fatalf("invalid PATCH changed store: stored=%#v snapshot=%#v", store.stored, store.snapshot)
			}
		})
	}
}

func TestSettingsPATCHAcceptsDatabaseCheckBoundaries(t *testing.T) {
	store := &fakeRuntimeSettingsStore{snapshot: runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
	}}
	body := `{
		"queue_capacity":10000,
		"queue_wait_timeout_ms":1000,
		"connect_timeout_ms":120000,
		"first_byte_timeout_ms":600000,
		"nonstream_total_timeout_ms":1800000,
		"shutdown_grace_ms":1000
	}`
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", body)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if len(store.stored) != 1 {
		t.Fatalf("stored snapshots = %d, want 1", len(store.stored))
	}
}

func TestSettingsPATCHStoresThenPublishesValidSnapshot(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
	}
	store := &fakeRuntimeSettingsStore{snapshot: current}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", `{"queue_capacity":9,"first_byte_timeout_ms":12000}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", response.Code, response.Body.String())
	}
	want := current
	want.QueueCapacity = 9
	want.FirstByteTimeoutMS = 12000
	if len(store.stored) != 1 || store.stored[0] != want || store.snapshot != want {
		t.Fatalf("stored = %#v, snapshot = %#v, want %#v", store.stored, store.snapshot, want)
	}
}

func TestSettingsPATCHDoesNotPublishWhenStoreFails(t *testing.T) {
	current := runtimeconfig.Snapshot{QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000, FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000}
	storeErr := errors.New("database unavailable")
	store := &fakeRuntimeSettingsStore{snapshot: current, err: storeErr}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", `{"queue_capacity":9}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("PATCH status = %d, want 500: %s", response.Code, response.Body.String())
	}
	if store.snapshot != current || len(store.stored) != 0 {
		t.Fatalf("failed PATCH changed store: %#v", store.snapshot)
	}
}

func serveSettingsRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/admin/api/settings"+path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
