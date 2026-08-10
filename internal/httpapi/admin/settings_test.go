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
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
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
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
	}
	// The "failover success code rejected" and "failover malformed range" cases
	// lock in the audit B4 §197 risk mitigation: a 200 token in the spec would
	// re-roll every successful response (waste key quota), and an inverted
	// range like "599-500" is a typo to reject at store time rather than
	// silently treating the matcher as "never fail over".
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
		{name: "retention below minimum", body: `{"request_log_retention_days":29}`},
		{name: "retention above maximum", body: `{"request_log_retention_days":366}`},
		{name: "failover success code rejected", body: `{"failover_status_codes":"200"}`},
		{name: "failover malformed range", body: `{"failover_status_codes":"599-500"}`},
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
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
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
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
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
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
	}
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

// TestSettingsPATCHStoresExpandedFailoverSpecAndRetentionDays confirms a
// legitimate operator patch — widening failover to include 403 and bumping
// retention to 60 days — round-trips through Validate and the store (audit B4
// happy path alongside B5 retention tuning).
// TestSettingsGETSerializesRetryBudgetMSWireName locks the retry_budget_ms wire
// name (audit R1.1): the front-end sends/reads retry_budget_ms, and the DTO must
// never fall back to the legacy retry_window_ms spelling that silently broke the
// runtime settings form under DisallowUnknownFields.
func TestSettingsGETSerializesRetryBudgetMSWireName(t *testing.T) {
	store := &fakeRuntimeSettingsStore{snapshot: runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
	}}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodGet, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if _, present := payload["data"]["retry_budget_ms"]; !present {
		t.Fatalf("GET response lacks retry_budget_ms: %s", response.Body.String())
	}
	if _, present := payload["data"]["retry_window_ms"]; present {
		t.Fatalf("GET response still exposes legacy retry_window_ms: %s", response.Body.String())
	}
}

// TestSettingsPATCHAcceptsRetryBudgetMSWireName is the regression test for the
// R1.1 contract break: before the fix the runtime settings form PATCH was
// rejected wholesale because decodeJSON enables DisallowUnknownFields.
func TestSettingsPATCHAcceptsRetryBudgetMSWireName(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
	}
	store := &fakeRuntimeSettingsStore{snapshot: current}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", `{"retry_budget_ms":150000}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", response.Code, response.Body.String())
	}
	want := current
	want.RetryBudgetMS = 150000
	if len(store.stored) != 1 || store.stored[0] != want || store.snapshot != want {
		t.Fatalf("stored = %#v, snapshot = %#v, want %#v", store.stored, store.snapshot, want)
	}
}

// TestSettingsPATCHRejectsLegacyRetryWindowMSWireName locks in that the legacy
// retry_window_ms spelling is rejected as an unknown field, so a stale front-end
// cannot silently patch nothing (which previously read as success with unchanged
// values because it was rejected before reaching the store).
func TestSettingsPATCHRejectsLegacyRetryWindowMSWireName(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
	}
	store := &fakeRuntimeSettingsStore{snapshot: current}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", `{"retry_window_ms":150000}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("legacy PATCH status = %d, want 400: %s", response.Code, response.Body.String())
	}
	if len(store.stored) != 0 || store.snapshot != current {
		t.Fatalf("legacy PATCH changed store: stored=%#v snapshot=%#v", store.stored, store.snapshot)
	}
}

func TestSettingsPATCHStoresExpandedFailoverSpecAndRetentionDays(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
	}
	store := &fakeRuntimeSettingsStore{snapshot: current}
	body := `{"failover_status_codes":"429,403,500-599","request_log_retention_days":60}`
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", body)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if len(store.stored) != 1 {
		t.Fatalf("stored snapshots = %d, want 1", len(store.stored))
	}
	want := current
	want.FailoverStatusCodes = "429,403,500-599"
	want.RequestLogRetentionDays = 60
	if store.stored[0] != want || store.snapshot != want {
		t.Fatalf("stored = %#v, snapshot = %#v, want %#v", store.stored[0], store.snapshot, want)
	}
}

// TestSettingsGETSerializesMaxStreamingPerKey verifies the R2.1 streaming quota
// setting round-trips on the wire under the documented max_streaming_per_key name.
func TestSettingsGETSerializesMaxStreamingPerKey(t *testing.T) {
	store := &fakeRuntimeSettingsStore{snapshot: runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000, MaxStreamingPerKey: 3,
	}}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodGet, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if value, present := payload["data"]["max_streaming_per_key"]; !present || int(value.(float64)) != 3 {
		t.Fatalf("GET response max_streaming_per_key = %v, want 3", payload["data"]["max_streaming_per_key"])
	}
}

// TestSettingsPATCHAcceptsMaxStreamingPerKey verifies an operator can tune the
// streaming quota through the settings PATCH endpoint.
func TestSettingsPATCHAcceptsMaxStreamingPerKey(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000, MaxStreamingPerKey: 2,
	}
	store := &fakeRuntimeSettingsStore{snapshot: current}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", `{"max_streaming_per_key":4}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", response.Code, response.Body.String())
	}
	want := current
	want.MaxStreamingPerKey = 4
	if len(store.stored) != 1 || store.stored[0] != want || store.snapshot != want {
		t.Fatalf("stored = %#v, snapshot = %#v, want %#v", store.stored, store.snapshot, want)
	}
}

// TestSettingsPATCHRejectsOutOfRangeMaxStreamingPerKey guards the 1..10 bound at
// the HTTP boundary, matching the database CHECK constraint. Zero is the
// pre-migration sentinel the pool resolves to the default, so it is not rejected
// here; values above the ceiling are.
func TestSettingsPATCHRejectsOutOfRangeMaxStreamingPerKey(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000, MaxStreamingPerKey: 2,
	}
	for _, body := range []string{`{"max_streaming_per_key":-1}`, `{"max_streaming_per_key":11}`} {
		store := &fakeRuntimeSettingsStore{snapshot: current}
		response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s status = %d, want 400", body, response.Code)
		}
		if len(store.stored) != 0 || store.snapshot != current {
			t.Fatalf("invalid PATCH changed store: stored=%#v snapshot=%#v", store.stored, store.snapshot)
		}
	}
}

// TestSettingsGETSerializesStreamTimeoutWireNames verifies the streaming timeout
// split round-trips on the wire under its documented names, so the front-end
// form and the contract snapshot stay in sync with the runtimeconfig snapshot.
func TestSettingsGETSerializesStreamTimeoutWireNames(t *testing.T) {
	store := &fakeRuntimeSettingsStore{snapshot: runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
		StreamFirstTokenTimeoutMS: 60000, StreamIdleTimeoutMS: 180000,
	}}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodGet, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if value, present := payload["data"]["stream_first_token_timeout_ms"]; !present || int(value.(float64)) != 60000 {
		t.Fatalf("GET response stream_first_token_timeout_ms = %v, want 60000", payload["data"]["stream_first_token_timeout_ms"])
	}
	if value, present := payload["data"]["stream_idle_timeout_ms"]; !present || int(value.(float64)) != 180000 {
		t.Fatalf("GET response stream_idle_timeout_ms = %v, want 180000", payload["data"]["stream_idle_timeout_ms"])
	}
}

// TestSettingsPATCHAcceptsStreamTimeoutWireNames verifies an operator can tune
// the streaming timeout split through the settings PATCH endpoint.
func TestSettingsPATCHAcceptsStreamTimeoutWireNames(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
		StreamFirstTokenTimeoutMS: 60000, StreamIdleTimeoutMS: 180000,
	}
	store := &fakeRuntimeSettingsStore{snapshot: current}
	response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", `{"stream_first_token_timeout_ms":90000,"stream_idle_timeout_ms":240000}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", response.Code, response.Body.String())
	}
	want := current
	want.StreamFirstTokenTimeoutMS = 90000
	want.StreamIdleTimeoutMS = 240000
	if len(store.stored) != 1 || store.stored[0] != want || store.snapshot != want {
		t.Fatalf("stored = %#v, snapshot = %#v, want %#v", store.stored, store.snapshot, want)
	}
}

// TestSettingsPATCHRejectsOutOfRangeStreamTimeouts guards the 1000..1800000
// bound at the HTTP boundary, matching the database CHECK constraints.
func TestSettingsPATCHRejectsOutOfRangeStreamTimeouts(t *testing.T) {
	current := runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000,
		StreamFirstTokenTimeoutMS: 60000, StreamIdleTimeoutMS: 180000,
	}
	for _, body := range []string{
		`{"stream_first_token_timeout_ms":999}`, `{"stream_first_token_timeout_ms":1800001}`,
		`{"stream_idle_timeout_ms":999}`, `{"stream_idle_timeout_ms":1800001}`,
	} {
		store := &fakeRuntimeSettingsStore{snapshot: current}
		response := serveSettingsRequest(t, NewSettings(store), http.MethodPatch, "", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s status = %d, want 400", body, response.Code)
		}
		if len(store.stored) != 0 || store.snapshot != current {
			t.Fatalf("invalid PATCH changed store: stored=%#v snapshot=%#v", store.stored, store.snapshot)
		}
	}
}
