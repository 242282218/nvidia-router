package config

import (
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		setenv  func(*testing.T)
		options LoadOptions
		check   func(*testing.T, Config)
		wantErr string
	}{
		{
			name: "rejects missing master key",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", "")
			},
			wantErr: "NVIDIA_ROUTER_MASTER_KEY",
		},
		{
			name: "uses port 3756 and NVIDIA default",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.ListenAddress != "0.0.0.0:3756" {
					t.Fatalf("ListenAddress = %q, want %q", cfg.ListenAddress, "0.0.0.0:3756")
				}
				if cfg.DataDir != "/data" {
					t.Fatalf("DataDir = %q, want %q", cfg.DataDir, "/data")
				}
				if cfg.TempDir != "/tmp" {
					t.Fatalf("TempDir = %q, want %q", cfg.TempDir, "/tmp")
				}
				if cfg.NVIDIABaseURL.String() != "https://integrate.api.nvidia.com" {
					t.Fatalf("NVIDIABaseURL = %q, want %q", cfg.NVIDIABaseURL, "https://integrate.api.nvidia.com")
				}
				if cfg.MasterKey != validMasterKeyBytes() {
					t.Fatalf("MasterKey = %v, want %v", cfg.MasterKey, validMasterKeyBytes())
				}
			},
		},
		{
			name: "uses environment overrides",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_LISTEN_ADDR", "127.0.0.1:8080")
				t.Setenv("NVIDIA_ROUTER_DATA_DIR", "data")
				t.Setenv("NVIDIA_ROUTER_TEMP_DIR", "temp")
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
				t.Setenv("NVIDIA_ROUTER_NVIDIA_BASE_URL", "https://example.com")
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.ListenAddress != "127.0.0.1:8080" {
					t.Fatalf("ListenAddress = %q, want %q", cfg.ListenAddress, "127.0.0.1:8080")
				}
				if cfg.DataDir != "data" {
					t.Fatalf("DataDir = %q, want %q", cfg.DataDir, "data")
				}
				if cfg.TempDir != "temp" {
					t.Fatalf("TempDir = %q, want %q", cfg.TempDir, "temp")
				}
				if cfg.NVIDIABaseURL.String() != "https://example.com" {
					t.Fatalf("NVIDIABaseURL = %q, want %q", cfg.NVIDIABaseURL, "https://example.com")
				}
			},
		},
		{
			name: "rejects invalid raw URL base64",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", "not valid raw base64!")
			},
			wantErr: "decode NVIDIA_ROUTER_MASTER_KEY",
		},
		{
			name: "rejects wrong master key length",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", base64.RawURLEncoding.EncodeToString([]byte("short")))
			},
			wantErr: "32 bytes",
		},
		{
			name: "allows HTTP only with test option",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
				t.Setenv("NVIDIA_ROUTER_NVIDIA_BASE_URL", "http://127.0.0.1:12345")
			},
			options: LoadOptions{AllowInsecureTestUpstream: true},
			check: func(t *testing.T, cfg Config) {
				if cfg.NVIDIABaseURL.String() != "http://127.0.0.1:12345" {
					t.Fatalf("NVIDIABaseURL = %q, want %q", cfg.NVIDIABaseURL, "http://127.0.0.1:12345")
				}
			},
		},
		{
			name: "rejects HTTP in production",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
				t.Setenv("NVIDIA_ROUTER_NVIDIA_BASE_URL", "http://127.0.0.1:12345")
			},
			wantErr: "HTTPS",
		},
		{
			name: "disables proxy when API URL is empty",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_API_URL", "")
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.XKProxyAPIURL != nil {
					t.Fatalf("XKProxyAPIURL = %v, want nil", cfg.XKProxyAPIURL)
				}
				if cfg.XKProxyTTL != 0 {
					t.Fatalf("XKProxyTTL = %v, want 0", cfg.XKProxyTTL)
				}
				if cfg.XKProxyRenewBefore != 0 {
					t.Fatalf("XKProxyRenewBefore = %v, want 0", cfg.XKProxyRenewBefore)
				}
			},
		},
		{
			name: "loads valid proxy configuration",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_API_URL", "http://proxy.example.test/tools/XApi.ashx?qty=1&apikey=fixture&sign=fixture")
			},
			check: func(t *testing.T, cfg Config) {
				proxyURL, ttl, renewBefore := proxyConfigFields(t, cfg)
				if proxyURL.String() != "http://proxy.example.test/tools/XApi.ashx?qty=1&apikey=fixture&sign=fixture" {
					t.Fatalf("proxy URL = %q", proxyURL)
				}
				if ttl != 3*time.Minute || renewBefore != 15*time.Second {
					t.Fatalf("proxy durations = %v/%v", ttl, renewBefore)
				}
			},
		},
		{
			name: "loads custom proxy durations",
			setenv: func(t *testing.T) {
				t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_API_URL", "https://proxy.example.test/x?qty=1")
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_TTL", "90s")
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE", "10s")
			},
			check: func(t *testing.T, cfg Config) {
				_, ttl, renewBefore := proxyConfigFields(t, cfg)
				if ttl != 90*time.Second || renewBefore != 10*time.Second {
					t.Fatalf("proxy durations = %v/%v", ttl, renewBefore)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			tt.setenv(t)

			cfg, err := LoadFromEnv(tt.options)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadFromEnv error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFromEnv: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadFromEnvRejectsInvalidProxyConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		ttl   string
		renew string
		want  string
	}{
		{name: "relative URL", url: "/proxy?qty=1", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "missing host", url: "http:///proxy?qty=1", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "unsupported scheme", url: "ftp://proxy.example.test/?qty=1", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "userinfo", url: "http://user:pass@proxy.example.test/?qty=1", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "fragment", url: "http://proxy.example.test/?qty=1#fragment", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "missing qty", url: "http://proxy.example.test/", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "duplicate qty", url: "http://proxy.example.test/?qty=1&qty=1", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "wrong qty", url: "http://proxy.example.test/?qty=2", want: "NVIDIA_ROUTER_XK_PROXY_API_URL"},
		{name: "short TTL", url: "http://proxy.example.test/?qty=1", ttl: "29s", want: "NVIDIA_ROUTER_XK_PROXY_TTL"},
		{name: "long TTL", url: "http://proxy.example.test/?qty=1", ttl: "31m", want: "NVIDIA_ROUTER_XK_PROXY_TTL"},
		{name: "invalid TTL", url: "http://proxy.example.test/?qty=1", ttl: "nope", want: "NVIDIA_ROUTER_XK_PROXY_TTL"},
		{name: "zero renew window", url: "http://proxy.example.test/?qty=1", renew: "0s", want: "NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE"},
		{name: "invalid renew format", url: "http://proxy.example.test/?qty=1", renew: "nope", want: "NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE"},
		{name: "renew window reaches TTL", url: "http://proxy.example.test/?qty=1", ttl: "30s", renew: "30s", want: "NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
			t.Setenv("NVIDIA_ROUTER_XK_PROXY_API_URL", test.url)
			if test.ttl != "" {
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_TTL", test.ttl)
			}
			if test.renew != "" {
				t.Setenv("NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE", test.renew)
			}

			_, err := LoadFromEnv(LoadOptions{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadFromEnv error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), test.url) || strings.Contains(err.Error(), "apikey") || strings.Contains(err.Error(), "sign") {
				t.Fatalf("proxy configuration error leaked sensitive URL/query: %v", err)
			}
		})
	}
}

func proxyConfigFields(t *testing.T, cfg Config) (*url.URL, time.Duration, time.Duration) {
	t.Helper()
	value := reflect.ValueOf(cfg)
	proxyField := value.FieldByName("XKProxyAPIURL")
	if !proxyField.IsValid() {
		t.Fatalf("XKProxyAPIURL field is missing")
	}
	proxyURL, ok := proxyField.Interface().(*url.URL)
	if !ok || proxyURL == nil {
		t.Fatalf("XKProxyAPIURL is missing or empty")
	}
	ttlField := value.FieldByName("XKProxyTTL")
	if !ttlField.IsValid() {
		t.Fatalf("XKProxyTTL field is missing")
	}
	ttl, ok := ttlField.Interface().(time.Duration)
	if !ok {
		t.Fatalf("XKProxyTTL is missing")
	}
	renewField := value.FieldByName("XKProxyRenewBefore")
	if !renewField.IsValid() {
		t.Fatalf("XKProxyRenewBefore field is missing")
	}
	renewBefore, ok := renewField.Interface().(time.Duration)
	if !ok {
		t.Fatalf("XKProxyRenewBefore is missing")
	}
	return proxyURL, ttl, renewBefore
}

func TestRequestBodyLimits(t *testing.T) {
	if JSONBodyLimit != 32<<20 {
		t.Fatalf("JSONBodyLimit = %d, want %d", JSONBodyLimit, 32<<20)
	}
	if ImageDecodedLimit != 20<<20 {
		t.Fatalf("ImageDecodedLimit = %d, want %d", ImageDecodedLimit, 20<<20)
	}
	if AudioBodyLimit != 25<<20 {
		t.Fatalf("AudioBodyLimit = %d, want %d", AudioBodyLimit, 25<<20)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"NVIDIA_ROUTER_LISTEN_ADDR",
		"NVIDIA_ROUTER_DATA_DIR",
		"NVIDIA_ROUTER_TEMP_DIR",
		"NVIDIA_ROUTER_MASTER_KEY",
		"NVIDIA_ROUTER_NVIDIA_BASE_URL",
		"NVIDIA_ROUTER_XK_PROXY_API_URL",
		"NVIDIA_ROUTER_XK_PROXY_TTL",
		"NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE",
	} {
		t.Setenv(name, "")
	}
}

func validMasterKey() string {
	key := validMasterKeyBytes()
	return base64.RawURLEncoding.EncodeToString(key[:])
}

func validMasterKeyBytes() [32]byte {
	var key [32]byte
	for index := range key {
		key[index] = byte(index)
	}
	return key
}
