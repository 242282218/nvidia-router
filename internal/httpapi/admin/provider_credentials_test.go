package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"nvidia-router/internal/providercredential"
)

type fakeProviderStore struct {
	items   []providercredential.Provider
	created providercredential.Provider
	enabled map[int64]bool
}

func (f *fakeProviderStore) List(context.Context) ([]providercredential.Provider, error) {
	return append([]providercredential.Provider(nil), f.items...), nil
}
func (f *fakeProviderStore) Create(_ context.Context, name, baseURL, key string) (providercredential.Provider, error) {
	f.created = providercredential.Provider{ID: 3, Name: name, BaseURL: baseURL, DisplayPrefix: key[:4], Enabled: false}
	return f.created, nil
}
func (f *fakeProviderStore) SetEnabled(_ context.Context, id int64, enabled bool) error {
	if f.enabled == nil {
		f.enabled = make(map[int64]bool)
	}
	f.enabled[id] = enabled
	return nil
}

func TestProviderCredentialsListAndCreateAndToggle(t *testing.T) {
	store := &fakeProviderStore{
		items: []providercredential.Provider{{ID: 1, Name: "siliconflow", BaseURL: "https://api.siliconflow.cn/v1", DisplayPrefix: "sk-a", DisplaySuffix: "xyz1", Enabled: true}},
	}
	handler := NewProviderCredentials(store)

	// List never exposes a token.
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/providers", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"siliconflow"`) || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}

	// Create with a name + key (plaintext key never echoes back).
	response = performAdminRequest(handler, http.MethodPost, "/admin/api/providers", `{"name":"siliconflow","base_url":"https://api.siliconflow.cn/v1","key":"sk-test-secret-key"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sk-test-secret-key") {
		t.Fatal("create response leaked provider key")
	}

	// Toggle enabled to false.
	response = performAdminRequest(handler, http.MethodPatch, "/admin/api/providers/1", `{"enabled":false}`)
	if response.Code != http.StatusOK {
		t.Fatalf("toggle status=%d body=%s", response.Code, response.Body.String())
	}
	if store.enabled[1] != false {
		t.Fatalf("enabled flag not recorded as false: %+v", store.enabled)
	}
}

func TestProviderCredentialsRejectsBadInput(t *testing.T) {
	handler := NewProviderCredentials(&fakeProviderStore{})
	for _, body := range []string{
		`{"name":"bad name!","base_url":"https://x","key":"k"}`,
		`{"name":"ok","base_url":"","key":"k"}`,
		`{"name":"ok","base_url":"https://x","key":""}`,
	} {
		response := performAdminRequest(handler, http.MethodPost, "/admin/api/providers", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("bad body %q status=%d, want 400", body, response.Code)
		}
	}
	for _, name := range []string{"ok", "ok_name-2"} {
		if !validProviderName(name) {
			t.Fatalf("valid name %q rejected", name)
		}
	}
	for _, name := range []string{"", "bad name", "has/slash", strings.Repeat("x", 33)} {
		if validProviderName(name) {
			t.Fatalf("invalid name %q accepted", name)
		}
	}
}

func TestProviderCreateRejectsUnsafeBaseURL(t *testing.T) {
	handler := NewProviderCredentials(&fakeProviderStore{})
	for _, baseURL := range []string{
		"ftp://api.example.test/v1",
		"https://user:password@api.example.test/v1",
		"https://api.example.test/v1?token=fixture",
		"https://api.example.test/v1#fragment",
		"https:///v1",
	} {
		response := performAdminRequest(handler, http.MethodPost, "/admin/api/providers", `{"name":"fixture","base_url":"`+baseURL+`","key":"fixture-provider-key"}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe base URL %q status=%d body=%s", baseURL, response.Code, response.Body.String())
		}
	}
}

func TestProviderEnableRejectsUnsupportedRuntime(t *testing.T) {
	store := &fakeProviderStore{}
	handler := NewProviderCredentials(store)
	response := performAdminRequest(handler, http.MethodPatch, "/admin/api/providers/7", `{"enabled":true}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsupported provider enable status=%d body=%s", response.Code, response.Body.String())
	}
	if _, called := store.enabled[7]; called {
		t.Fatal("unsupported provider was passed to the store")
	}
}
