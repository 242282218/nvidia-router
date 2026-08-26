package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/xkproxy"
)

type fakeSummary struct{}

func (fakeSummary) Summary() pool.Summary {
	return pool.Summary{
		Keys:   pool.KeyStatusCounts{Total: 3, Enabled: 2, CoolingDown: 1, Ready: 1},
		Active: 1,
		Queue:  pool.QueueSummary{Length: 4, Capacity: 100},
	}
}

type fakeRequests struct{}

func (fakeRequests) MetricsSummary(context.Context) (observability.MetricsSummary, error) {
	return observability.MetricsSummary{Requests: 12, Successes: 10, Failures: 2}, nil
}

type fakeRequestBuffer struct{}

func (fakeRequestBuffer) Depth() int         { return 3 }
func (fakeRequestBuffer) Dropped() int64     { return 4 }
func (fakeRequestBuffer) FlushFailed() int64 { return 5 }

type fakeProxyPool struct{}

func (fakeProxyPool) PoolStatus() xkproxy.PoolStatus {
	return xkproxy.PoolStatus{TotalSize: 9, HealthySize: 7, PanicMode: true, UpstreamOverloaded: true}
}

func TestMetricsHandlerExposesPrometheusText(t *testing.T) {
	handler := New(fakeSummary{}, fakeRequests{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("Content-Type = %q", got)
	}
	body := response.Body.String()
	for _, want := range []string{
		"nvidia_router_up 1",
		"nvidia_router_keys_total 3",
		"nvidia_router_queue_length 4",
		"nvidia_router_requests_total 12",
		"nvidia_router_requests_succeeded_total 10",
		"nvidia_router_requests_failed_total 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsHandlerExposesProxyPoolHealth(t *testing.T) {
	handler := New(fakeSummary{}, fakeRequests{}).WithProxyPool(fakeProxyPool{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := response.Body.String()
	for _, want := range []string{
		"nvidia_router_proxy_pool_total 9",
		"nvidia_router_proxy_pool_healthy 7",
		"nvidia_router_proxy_pool_panic_mode 1",
		"nvidia_router_proxy_pool_upstream_overloaded 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsHandlerExposesRequestBufferHealth(t *testing.T) {
	handler := New(fakeSummary{}, fakeRequests{}, fakeRequestBuffer{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := response.Body.String()
	for _, want := range []string{
		"nvidia_router_request_log_buffer_depth 3",
		"nvidia_router_request_log_dropped_total 4",
		"nvidia_router_request_log_flush_failed_total 5",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsHandlerRejectsNonGet(t *testing.T) {
	handler := New(fakeSummary{}, fakeRequests{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
}
