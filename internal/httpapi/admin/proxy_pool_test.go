package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/xkproxy"
)

func TestProxyPoolHandlerReturnsSafeStatusAndForwardsPatch(t *testing.T) {
	service := &fakeProxyPoolService{snapshot: xkproxy.Snapshot{
		Enabled: true, ProxyURL: "http://proxy-pool:8080", AuthConfigured: true, Source: xkproxy.SourceDatabase,
	}}
	handler := NewProxyPool(service)

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/admin/api/proxy-pool", nil))
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), "proxy-secret") {
		t.Fatalf("GET status/body = %d/%s", get.Code, get.Body.String())
	}
	var body struct {
		Data xkproxy.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if body.Data != service.snapshot {
		t.Fatalf("GET data = %#v, want %#v", body.Data, service.snapshot)
	}

	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, "/admin/api/proxy-pool", strings.NewReader(`{"enabled":false,"proxy_url":"http://proxy-pool:8081","auth_key":"proxy-secret"}`)))
	if patch.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", patch.Code, patch.Body.String())
	}
	if service.patch.AuthKey != "proxy-secret" || service.patch.Enabled == nil || *service.patch.Enabled {
		t.Fatalf("patch = %#v", service.patch)
	}
}

func TestProxyPoolHandlerRejectsInvalidMethodAndDoesNotExposeServiceError(t *testing.T) {
	handler := NewProxyPool(&fakeProxyPoolService{snapshot: xkproxy.Snapshot{}, updateErr: errors.New("secret must not leak")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/admin/api/proxy-pool", strings.NewReader(`{"enabled":true}`)))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("PATCH status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret must not leak") {
		t.Fatal("service error leaked into API response")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/api/proxy-pool", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("POST status = %d, want 404", response.Code)
	}
}

type fakeProxyPoolService struct {
	snapshot  xkproxy.Snapshot
	patch     xkproxy.Patch
	updateErr error
}

func (f *fakeProxyPoolService) Snapshot(context.Context) (xkproxy.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeProxyPoolService) Update(_ context.Context, patch xkproxy.Patch) (xkproxy.Snapshot, error) {
	f.patch = patch
	if f.updateErr != nil {
		return xkproxy.Snapshot{}, f.updateErr
	}
	return f.snapshot, nil
}
