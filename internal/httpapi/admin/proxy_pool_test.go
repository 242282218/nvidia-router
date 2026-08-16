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

func TestProxyPoolHandlerPreservesPatchFieldPresence(t *testing.T) {
	service := &fakeProxyPoolService{snapshot: xkproxy.Snapshot{Enabled: false}}
	handler := NewProxyPool(service)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/admin/api/proxy-pool", strings.NewReader(`{"enabled":false}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("omitted fields status = %d: %s", response.Code, response.Body.String())
	}
	if service.patch.ValidationURL != nil || service.patch.ExpectedQty != nil || service.patch.MaxLatency != nil {
		t.Fatalf("omitted fields were populated: %#v", service.patch)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/admin/api/proxy-pool", strings.NewReader(`{"validation_url":"","expected_qty":0,"max_latency":""}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("explicit clear status = %d: %s", response.Code, response.Body.String())
	}
	if service.patch.ValidationURL == nil || *service.patch.ValidationURL != "" || service.patch.ExpectedQty == nil || *service.patch.ExpectedQty != 0 || service.patch.MaxLatency == nil || *service.patch.MaxLatency != "" {
		t.Fatalf("explicit clear fields were not preserved: %#v", service.patch)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/admin/api/proxy-pool", strings.NewReader(`{"upstream_url":"http://127.0.0.1:2375/metadata"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("upstream URL status = %d, want 200", response.Code)
	}
	if service.patch.UpstreamURL == nil || *service.patch.UpstreamURL != "http://127.0.0.1:2375/metadata" {
		t.Fatalf("upstream URL patch = %#v", service.patch)
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

func TestProxyPoolHandlerMapsValidationErrorsToBadRequest(t *testing.T) {
	handler := NewProxyPool(&fakeProxyPoolService{snapshot: xkproxy.Snapshot{}, updateErr: &xkproxy.ValidationError{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/admin/api/proxy-pool", strings.NewReader(`{"enabled":false}`)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_proxy_pool") {
		t.Fatalf("validation status/body = %d/%s", response.Code, response.Body.String())
	}
}

type fakeProxyPoolService struct {
	snapshot   xkproxy.Snapshot
	patch      xkproxy.Patch
	updateErr  error
	status     xkproxy.PoolStatus
	refreshed  bool
	refreshErr error
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

func (f *fakeProxyPoolService) Refresh(context.Context) error {
	f.refreshed = true
	return f.refreshErr
}

func TestProxyPoolHandlerRefreshesBuiltInCollector(t *testing.T) {
	service := &fakeProxyPoolService{refreshErr: errors.New("collector unavailable")}
	handler := NewProxyPool(service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/api/proxy-pool/refresh", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502", response.Code)
	}
	if !service.refreshed {
		t.Fatal("collector refresh was not called")
	}
}

func TestProxyPoolHandlerStatusEndpoint(t *testing.T) {
	service := &fakeProxyPoolService{status: xkproxy.PoolStatus{
		TotalSize: 3, HealthySize: 2, UpstreamOverloaded: true,
		LastUpstreamOverloadAt: time.Date(2026, time.August, 12, 4, 5, 0, 0, time.UTC),
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
			TotalSize              int                   `json:"total_size"`
			HealthySize            int                   `json:"healthy_size"`
			UpstreamOverloaded     bool                  `json:"upstream_overloaded"`
			LastUpstreamOverloadAt string                `json:"last_upstream_overload_at"`
			Proxies                []xkproxy.ProxyStatus `json:"proxies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.Data.TotalSize != 3 || body.Data.HealthySize != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", body.Data.TotalSize, body.Data.HealthySize)
	}
	if !body.Data.UpstreamOverloaded || body.Data.LastUpstreamOverloadAt != "2026-08-12T04:05:00Z" {
		t.Fatalf("upstream overload = %v at %q, want true at 2026-08-12T04:05:00Z", body.Data.UpstreamOverloaded, body.Data.LastUpstreamOverloadAt)
	}
	if len(body.Data.Proxies) != 2 {
		t.Fatalf("proxies len = %d, want 2", len(body.Data.Proxies))
	}
	if body.Data.Proxies[0].LatencyEWMAMS != 150 {
		t.Fatalf("latency = %d, want 150", body.Data.Proxies[0].LatencyEWMAMS)
	}
}

func TestProxyPoolHandlerStatusExposesRequestQualityWithoutCredentials(t *testing.T) {
	service := &fakeProxyPoolService{status: xkproxy.PoolStatus{
		Proxies: []xkproxy.Proxy{{
			Scheme:                "http",
			Address:               "10.0.0.1:8080",
			Username:              "hidden-user",
			Password:              "hidden-password",
			RequestSuccessCount:   4,
			RequestFailureCount:   1,
			RequestLatencyEWMA:    180 * time.Millisecond,
			RequestLatencySamples: 5,
			ExpiresAt:             time.Now().Add(time.Minute),
		}},
	}}
	handler := NewProxyPool(service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/proxy-pool/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			Proxies []struct {
				QualityScore          int    `json:"quality_score"`
				RequestSuccessCount   uint64 `json:"request_success_count"`
				RequestFailureCount   uint64 `json:"request_failure_count"`
				RequestLatencyEWMAMS  int64  `json:"request_latency_ewma_ms"`
				RequestLatencySamples int    `json:"request_latency_samples"`
			} `json:"proxies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if len(body.Data.Proxies) != 1 {
		t.Fatalf("proxies len = %d, want 1", len(body.Data.Proxies))
	}
	proxy := body.Data.Proxies[0]
	if proxy.QualityScore == 0 || proxy.RequestSuccessCount != 4 || proxy.RequestFailureCount != 1 || proxy.RequestLatencyEWMAMS != 180 || proxy.RequestLatencySamples != 5 {
		t.Fatalf("quality projection = %+v", proxy)
	}
	if strings.Contains(response.Body.String(), "hidden-password") || strings.Contains(response.Body.String(), "hidden-user") {
		t.Fatal("proxy credentials leaked in status response")
	}
}
