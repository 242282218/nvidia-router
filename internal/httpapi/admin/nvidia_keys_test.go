package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/keystate"
	"nvidia-router/internal/nvidiakey"
)

type fakeNVIDIAKeys struct {
	keys         []nvidiakey.Key
	importResult nvidiakey.ImportResult
	batch        []nvidiakey.ImportResult
	tests        []nvidiakey.TestResult
	activeTests  int
	maxTests     int
	mu           sync.Mutex
}

func (f *fakeNVIDIAKeys) List(context.Context) ([]nvidiakey.Key, error) {
	return append([]nvidiakey.Key(nil), f.keys...), nil
}
func (f *fakeNVIDIAKeys) Import(context.Context, string) (nvidiakey.ImportResult, error) {
	return f.importResult, nil
}
func (f *fakeNVIDIAKeys) ImportBatch(context.Context, string) []nvidiakey.ImportResult {
	return append([]nvidiakey.ImportResult(nil), f.batch...)
}
func (f *fakeNVIDIAKeys) SetEnabled(_ context.Context, id int64, enabled bool) (keystate.KeySnapshot, error) {
	return keystate.KeySnapshot{ID: id, Enabled: enabled}, nil
}
func (f *fakeNVIDIAKeys) Delete(context.Context, int64) error { return nil }
func (f *fakeNVIDIAKeys) Test(_ context.Context, id int64) (nvidiakey.TestResult, error) {
	f.mu.Lock()
	f.activeTests++
	if f.activeTests > f.maxTests {
		f.maxTests = f.activeTests
	}
	f.mu.Unlock()
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	f.activeTests--
	f.mu.Unlock()
	for _, result := range f.tests {
		if result.ID == id {
			return result, nil
		}
	}
	return nvidiakey.TestResult{ID: id, Status: "valid"}, nil
}

type fakeStateSync struct {
	upserts []keystate.KeySnapshot
	removed []int64
	blocks  [][3]int64
}

func (*fakeStateSync) LoadSnapshot([]keystate.KeySnapshot, []keystate.ModelBlock)   {}
func (s *fakeStateSync) UpsertKey(k keystate.KeySnapshot)                           { s.upserts = append(s.upserts, k) }
func (s *fakeStateSync) RemoveKey(id int64)                                         { s.removed = append(s.removed, id) }
func (*fakeStateSync) ApplySuccess(int64)                                           {}
func (*fakeStateSync) ApplyFailure(int64, int64, interface{}, keystate.KeySnapshot) {}
func (*fakeStateSync) SetModelEnabled(int64, bool)                                  {}
func (*fakeStateSync) ClearModelBlocks(int64)                                       {}
func (s *fakeStateSync) SetModelBlock(k, m int64, b bool) {
	v := int64(0)
	if b {
		v = 1
	}
	s.blocks = append(s.blocks, [3]int64{k, m, v})
}

func TestNVIDIAKeyAPIUsesAllowlistedDTOAndSynchronizesState(t *testing.T) {
	secret := "nvapi-secret-should-never-appear"
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	service := &fakeNVIDIAKeys{keys: []nvidiakey.Key{{ID: 7, DisplayPrefix: "nvapi-", DisplaySuffix: "tail", Enabled: true, CreatedAt: now, UpdatedAt: now}}, importResult: nvidiakey.ImportResult{Status: nvidiakey.ImportStatusImported, Masked: "nvapi-…tail", Key: &nvidiakey.Key{ID: 8, Enabled: true}}}
	syncer := &fakeStateSync{}
	handler := NewNVIDIAKeys(service, syncer)

	response := performAdminRequest(handler, http.MethodGet, "/admin/api/nvidia-keys", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{secret, "ciphertext", "nonce", "fingerprint", "digest"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{`"id":7`, `"masked":"nvapi-…tail"`, `"enabled":true`} {
		if !strings.Contains(body, required) {
			t.Fatalf("response missing %s: %s", required, body)
		}
	}

	response = performAdminRequest(handler, http.MethodPost, "/admin/api/nvidia-keys", `{"key":"`+secret+`"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatalf("import response leaked plaintext: %s", response.Body.String())
	}
	if len(syncer.upserts) != 1 || syncer.upserts[0].ID != 8 {
		t.Fatalf("sync upserts=%+v", syncer.upserts)
	}

	response = performAdminRequest(handler, http.MethodPatch, "/admin/api/nvidia-keys/7", `{"enabled":false}`)
	if response.Code != http.StatusOK || len(syncer.upserts) != 2 || syncer.upserts[1].Enabled {
		t.Fatalf("patch response=%d sync=%+v", response.Code, syncer.upserts)
	}
	response = performAdminRequest(handler, http.MethodDelete, "/admin/api/nvidia-keys/7", "")
	if response.Code != http.StatusNoContent || len(syncer.removed) != 1 || syncer.removed[0] != 7 {
		t.Fatalf("delete response=%d removed=%v", response.Code, syncer.removed)
	}
}

func TestNVIDIAKeyBatchAndTestAllAreSequential(t *testing.T) {
	service := &fakeNVIDIAKeys{keys: []nvidiakey.Key{{ID: 1}, {ID: 2}, {ID: 3}}, batch: []nvidiakey.ImportResult{{Line: 1, Status: nvidiakey.ImportStatusImported, Masked: "a…z", Key: &nvidiakey.Key{ID: 4, Enabled: true}}, {Line: 2, Status: nvidiakey.ImportStatusInvalid, Reason: "invalid_format", Masked: "invalid"}}, tests: []nvidiakey.TestResult{{ID: 1, Status: "valid"}, {ID: 2, Status: "temporarily_unavailable"}, {ID: 3, Status: "invalid"}}}
	syncer := &fakeStateSync{}
	handler := NewNVIDIAKeys(service, syncer)
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/nvidia-keys/batch", `{"keys":"one\ntwo"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"line":2`) || len(syncer.upserts) != 1 {
		t.Fatalf("batch status=%d body=%s sync=%+v", response.Code, response.Body.String(), syncer.upserts)
	}
	response = performAdminRequest(handler, http.MethodPost, "/admin/api/nvidia-keys/test-all", "")
	if response.Code != http.StatusOK || service.maxTests != 1 {
		t.Fatalf("test-all status=%d max concurrency=%d body=%s", response.Code, service.maxTests, response.Body.String())
	}
	var envelope struct {
		Data []nvidiakey.TestResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 3 || envelope.Data[0].ID != 1 || envelope.Data[2].ID != 3 {
		t.Fatalf("test order=%+v", envelope.Data)
	}
}

func performAdminRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
