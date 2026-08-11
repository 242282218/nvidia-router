package xkproxy

import (
	"testing"
	"time"
)

func TestParseProxy(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "simple host:port",
			input: "192.168.1.1:8080",
			want:  "192.168.1.1:8080",
		},
		{
			name:  "with credentials",
			input: "192.168.1.1:8080:user:pass",
			want:  "192.168.1.1:8080",
		},
		{
			name:  "http URL",
			input: "http://192.168.1.1:8080",
			want:  "192.168.1.1:8080",
		},
		{
			name:  "http URL with auth",
			input: "http://user:pass@192.168.1.1:8080",
			want:  "192.168.1.1:8080",
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid port",
			input:   "192.168.1.1:99999",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy, err := ParseProxy(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseProxy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && proxy.Address != tt.want {
				t.Errorf("ParseProxy() address = %v, want %v", proxy.Address, tt.want)
			}
		})
	}
}

func TestProxyLifecycle(t *testing.T) {
	now := time.Now()
	proxy := Proxy{
		Address:   "192.168.1.1:8080",
		ExpiresAt: now.Add(1 * time.Minute),
	}

	if !proxy.LiveAt(now) {
		t.Error("Expected proxy to be live")
	}

	if !proxy.LiveAt(now.Add(30 * time.Second)) {
		t.Error("Expected proxy to be live after 30s")
	}

	if proxy.LiveAt(now.Add(2 * time.Minute)) {
		t.Error("Expected proxy to be expired after 2m")
	}

	// Test ejection
	proxy.EjectedUntil = now.Add(10 * time.Second)
	if proxy.AvailableAt(now) {
		t.Error("Expected proxy to be unavailable when ejected")
	}

	if !proxy.AvailableAt(now.Add(11 * time.Second)) {
		t.Error("Expected proxy to be available after ejection expires")
	}
}

func TestPoolOperations(t *testing.T) {
	pool := NewPool()
	now := time.Now()

	proxies := []Proxy{
		{
			Address:     "192.168.1.1:8080",
			FetchedAt:   now,
			ValidatedAt: now,
			ExpiresAt:   now.Add(2 * time.Minute),
		},
		{
			Address:     "192.168.1.2:8080",
			FetchedAt:   now,
			ValidatedAt: now,
			ExpiresAt:   now.Add(2 * time.Minute),
		},
	}

	pool.Merge(now, proxies)

	if size := pool.Size(now); size != 2 {
		t.Errorf("Expected pool size 2, got %d", size)
	}

	proxy, ok := pool.Get(now)
	if !ok {
		t.Fatal("Expected to get a proxy")
	}
	if proxy.Address == "" {
		t.Error("Expected non-empty proxy address")
	}

	// Test expiration
	expiredTime := now.Add(3 * time.Minute)
	if size := pool.Size(expiredTime); size != 0 {
		t.Errorf("Expected pool size 0 after expiration, got %d", size)
	}
}

func TestPoolEjection(t *testing.T) {
	pool := NewPool()
	now := time.Now()

	proxy := Proxy{
		Address:     "192.168.1.1:8080",
		FetchedAt:   now,
		ValidatedAt: now,
		ExpiresAt:   now.Add(2 * time.Minute),
	}

	pool.Merge(now, []Proxy{proxy})

	policy := EjectionPolicy{
		FailureLimit: 3,
		BaseDuration: 10 * time.Second,
		MaxDuration:  60 * time.Second,
		MaxEjections: 3,
	}

	// Report failures
	outcome := pool.ReportFailure(proxy.Address, now, policy)
	if outcome != OutcomeCounted {
		t.Errorf("Expected OutcomeCounted, got %v", outcome)
	}

	outcome = pool.ReportFailure(proxy.Address, now, policy)
	if outcome != OutcomeCounted {
		t.Errorf("Expected OutcomeCounted, got %v", outcome)
	}

	outcome = pool.ReportFailure(proxy.Address, now, policy)
	if outcome != OutcomeEjected {
		t.Errorf("Expected OutcomeEjected, got %v", outcome)
	}

	// Proxy should be unavailable
	if size := pool.Size(now); size != 0 {
		t.Errorf("Expected pool size 0 after ejection, got %d", size)
	}

	// Report success should recover
	pool.ReportSuccess(proxy.Address, now, 100*time.Millisecond, policy)
	if size := pool.Size(now.Add(11 * time.Second)); size != 1 {
		t.Errorf("Expected pool size 1 after recovery, got %d", size)
	}
}

func TestParseProxies(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{
			name:      "single proxy",
			input:     "192.168.1.1:8080",
			wantCount: 1,
		},
		{
			name:      "multiple proxies",
			input:     "192.168.1.1:8080\n192.168.1.2:8080",
			wantCount: 2,
		},
		{
			name:      "with empty lines",
			input:     "192.168.1.1:8080\n\n192.168.1.2:8080",
			wantCount: 2,
		},
		{
			name:      "deduplicate",
			input:     "192.168.1.1:8080\n192.168.1.1:8080",
			wantCount: 1,
		},
		{
			name:    "provider error",
			input:   "code: 204",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxies, err := ParseProxies(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseProxies() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(proxies) != tt.wantCount {
				t.Errorf("ParseProxies() count = %d, want %d", len(proxies), tt.wantCount)
			}
		})
	}
}

func TestProviderErrorDetection(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCode string
	}{
		{
			name:     "code 204",
			input:    "code: 204",
			wantCode: "204",
		},
		{
			name:     "status 403",
			input:    "status: 403",
			wantCode: "403",
		},
		{
			name:     "error 211",
			input:    "error 211: whitelist required",
			wantCode: "211",
		},
		{
			name:     "no error",
			input:    "192.168.1.1:8080",
			wantCode: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProxies(tt.input)
			code := ErrorCode(err)
			if code != tt.wantCode {
				t.Errorf("ErrorCode() = %v, want %v", code, tt.wantCode)
			}
		})
	}
}
