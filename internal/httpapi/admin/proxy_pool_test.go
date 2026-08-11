package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	status    xkproxy.PoolStatus
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

func (f *fakeProxyPoolService) PoolStatus() xkproxy.PoolStatus {
	return f.status
}

func TestProxyPoolHandlerStatusEndpoint(t *testing.T) {
	service := &fakeProxyPoolService{status: xkproxy.PoolStatus{
		TotalSize: 3, HealthySize: 2,
		Proxies: []xkproxy.Proxy{
			{Address: "10.0.0.1:8080", LatencyEWMA: 150 * time.Millisecond, SuccessCount: 5, ExpiresAt: time.Now().Add(time.Minute)},
			{Address: "10.0.0.2:8080", LatencyEWMA: 800 * time.Millisecond, FailureCount: 2},
		},
	}}
	handler := NewProxyPool(service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/proxy-pool/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			TotalSize   int `json:"total_size"`
			HealthySize int `json:"healthy_size"`
			Proxies     []xkproxy.ProxyStatus `json:"proxies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.Data.TotalSize != 3 || body.Data.HealthySize != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", body.Data.TotalSize, body.Data.HealthySize)
	}
	if len(body.Data.Proxies) != 2 {
		t.Fatalf("proxies len = %d, want 2", len(body.Data.Proxies))
	}
	if body.Data.Proxies[0].LatencyEWMAMS != 150 {
		t.Fatalf("latency = %d, want 150", body.Data.Proxies[0].LatencyEWMAMS)
	}
}
