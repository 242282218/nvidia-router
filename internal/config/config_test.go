package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testInitialAdminPassword = "test-initial-admin-password"

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
				setBaseConfigEnv(t)
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.ListenAddress != "0.0.0.0:3756" {
					t.Fatalf("ListenAddress = %q, want %q", cfg.ListenAddress, "0.0.0.0:3756")
				}
				if cfg.DataDir != "/data" || cfg.TempDir != "/tmp" {
					t.Fatalf("directories = %q/%q, want /data//tmp", cfg.DataDir, cfg.TempDir)
				}
				if cfg.NVIDIABaseURL.String() != "https://integrate.api.nvidia.com" {
					t.Fatalf("NVIDIABaseURL = %q", cfg.NVIDIABaseURL)
				}
				if cfg.MasterKey != validMasterKeyBytes() || cfg.MasterKeyVersion != 1 || cfg.LegacyMasterKey != nil {
					t.Fatalf("master key config = %#v", cfg)
				}
			},
		},
		{
			name: "requires secure cookie for HTTPS external origin",
			setenv: func(t *testing.T) {
				setBaseConfigEnv(t)
				t.Setenv("NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN", "https://admin.example.test")
			},
			wantErr: "ADMIN_SECURE_COOKIE",
		},
		{
			name: "loads HTTPS external origin with secure cookie",
			setenv: func(t *testing.T) {
				setBaseConfigEnv(t)
				t.Setenv("NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN", "https://admin.example.test")
				t.Setenv("NVIDIA_ROUTER_ADMIN_SECURE_COOKIE", "true")
				t.Setenv("NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.AdminExternalOrigin.String() != "https://admin.example.test" || !cfg.AdminSecureCookie || len(cfg.TrustedProxyCIDRs) != 1 {
					t.Fatalf("origin security config = %#v/%v/%v", cfg.AdminExternalOrigin, cfg.AdminSecureCookie, cfg.TrustedProxyCIDRs)
				}
			},
		},
		{
			name: "uses environment overrides",
			setenv: func(t *testing.T) {
				setBaseConfigEnv(t)
				t.Setenv("NVIDIA_ROUTER_LISTEN_ADDR", "127.0.0.1:8080")
				t.Setenv("NVIDIA_ROUTER_DATA_DIR", "data")
				t.Setenv("NVIDIA_ROUTER_TEMP_DIR", "temp")
				t.Setenv("NVIDIA_ROUTER_NVIDIA_BASE_URL", "https://example.com")
				t.Setenv("NVIDIA_ROUTER_OPENCODEFREE_BASE_URL", "http://opencode-free-proxy:6020/v1")
				t.Setenv("NVIDIA_ROUTER_OPENCODEFREE_AUTH_KEY", "entry-auth")
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.ListenAddress != "127.0.0.1:8080" || cfg.DataDir != "data" || cfg.TempDir != "temp" {
					t.Fatalf("overrides = %q/%q/%q", cfg.ListenAddress, cfg.DataDir, cfg.TempDir)
				}
				if cfg.NVIDIABaseURL.String() != "https://example.com" {
					t.Fatalf("NVIDIABaseURL = %q", cfg.NVIDIABaseURL)
				}
				if cfg.OpenCodeFreeBaseURL.String() != "http://opencode-free-proxy:6020/v1" || cfg.OpenCodeFreeAuthKey != "entry-auth" {
					t.Fatalf("OpenCodeFree config = %v/%q", cfg.OpenCodeFreeBaseURL, cfg.OpenCodeFreeAuthKey)
				}
			},
		},
		{
			name:    "rejects invalid raw URL base64",
			setenv:  func(t *testing.T) { t.Setenv("NVIDIA_ROUTER_MASTER_KEY", "not valid raw base64!") },
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
				setBaseConfigEnv(t)
				t.Setenv("NVIDIA_ROUTER_NVIDIA_BASE_URL", "http://127.0.0.1:12345")
			},
			options: LoadOptions{AllowInsecureTestUpstream: true},
			check: func(t *testing.T, cfg Config) {
				if cfg.NVIDIABaseURL.String() != "http://127.0.0.1:12345" {
					t.Fatalf("NVIDIABaseURL = %q", cfg.NVIDIABaseURL)
				}
			},
		},
		{
			name: "rejects HTTP in production",
			setenv: func(t *testing.T) {
				setBaseConfigEnv(t)
				t.Setenv("NVIDIA_ROUTER_NVIDIA_BASE_URL", "http://127.0.0.1:12345")
			},
			wantErr: "HTTPS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			tt.setenv(t)
			cfg, err := LoadFromEnv(tt.options)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadFromEnv error = %v, want %q", err, tt.wantErr)
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
	tests := []struct{ name, rawURL, want string }{
		{"relative URL", "/proxy", "NVIDIA_ROUTER_XK_PROXY_URL"},
		{"missing host", "http:///proxy", "NVIDIA_ROUTER_XK_PROXY_URL"},
		{"unsupported scheme", "ftp://proxy.example.test/", "NVIDIA_ROUTER_XK_PROXY_URL"},
		{"userinfo", "http://user:pass@proxy.example.test:8080", "NVIDIA_ROUTER_XK_PROXY_URL"},
		{"query", "http://proxy.example.test:8080?x=1", "NVIDIA_ROUTER_XK_PROXY_URL"},
		{"fragment", "http://proxy.example.test:8080#fragment", "NVIDIA_ROUTER_XK_PROXY_URL"},
		{"path", "http://proxy.example.test:8080/path", "NVIDIA_ROUTER_XK_PROXY_URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			setBaseConfigEnv(t)
			t.Setenv("NVIDIA_ROUTER_XK_PROXY_URL", test.rawURL)
			t.Setenv("NVIDIA_ROUTER_XK_PROXY_AUTH_KEY", "proxy-secret")
			_, err := LoadFromEnv(LoadOptions{})
			if err == nil || !strings.Contains(err.Error(), "external Xingkong proxy settings are unsupported") {
				t.Fatalf("LoadFromEnv error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadFromEnvRejectsStaticProxyPoolConfiguration(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_PROXY_URL", "http://proxy-pool:8080")
	t.Setenv("NVIDIA_ROUTER_XK_PROXY_AUTH_KEY", "proxy-secret")

	_, err := LoadFromEnv(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "external Xingkong proxy settings are unsupported") {
		t.Fatalf("LoadFromEnv error = %v, want external mode rejection", err)
	}
}

func TestLoadFromEnvRejectsProxyPoolWithoutAuthKey(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_PROXY_URL", "http://proxy-pool:8080")

	_, err := LoadFromEnv(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "external Xingkong proxy settings are unsupported") {
		t.Fatalf("LoadFromEnv error = %v, want external mode rejection", err)
	}
}

func TestLoadFromEnvLoadsBuiltInProxyPoolConfiguration(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "http://xapi.example.test/tools/XApi.ashx?apikey=test")

	cfg, err := LoadFromEnv(LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.XKPool == nil || cfg.XKPool.UpstreamURL != "http://xapi.example.test/tools/XApi.ashx?apikey=test" {
		t.Fatalf("XKPool = %#v, want built-in collector config", cfg.XKPool)
	}
}

func TestLoadFromEnvRejectsInlineXKUpstreamConfiguration(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "http://xapi.example.test/tools/XApi.ashx?apikey=test")

	_, err := LoadFromEnv(LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
}

func TestLoadFromEnvRejectsStaticAndPoolProxyTogether(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_PROXY_URL", "http://proxy-pool:8080")
	t.Setenv("NVIDIA_ROUTER_XK_PROXY_AUTH_KEY", "proxy-secret")
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "https://api.xingkongdaili.com/api/getproxy/123?apikey=test")

	_, err := LoadFromEnv(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "external Xingkong proxy settings are unsupported") {
		t.Fatalf("LoadFromEnv error = %v, want mutually exclusive proxy modes", err)
	}
}

func TestLoadFromEnvRejectsPrivateHostInXKUpstreamURL(t *testing.T) {
	cases := []string{
		"http://127.0.0.1:8080/tools/XApi.ashx?apikey=x",
		"http://169.254.169.254/latest/meta-data/?apikey=x",
		"http://10.0.0.1:80/x?apikey=x",
		"http://192.168.1.1/x?apikey=x",
		"http://[::1]:8080/x?apikey=x",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			clearConfigEnv(t)
			setBaseConfigEnv(t)
			t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", raw)
			_, err := LoadFromEnv(LoadOptions{})
			if err == nil || !strings.Contains(err.Error(), "must not target a private or loopback IP") {
				t.Fatalf("LoadFromEnv error = %v, want private-IP rejection", err)
			}
		})
	}
}

func TestLoadFromEnvAllowsPublicHostInXKUpstreamURL(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "http://api.xingkongdaili.com/api/getproxy/123?apikey=test")
	cfg, err := LoadFromEnv(LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.XKPool == nil {
		t.Fatal("XKPool = nil, want configured collector")
	}
}

func TestLoadFromEnvRejectsPrivateHostInXKValidationURL(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "http://api.xingkongdaili.com/api/getproxy/123?apikey=test")
	t.Setenv("NVIDIA_ROUTER_XK_VALIDATION_URL", "http://127.0.0.1:9999/")
	_, err := LoadFromEnv(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "must not target a private or loopback IP") {
		t.Fatalf("LoadFromEnv error = %v, want private-IP rejection for validation URL", err)
	}
}

func TestLoadFromEnvRejectsInvalidPoolValidationStatus(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "https://api.xingkongdaili.com/api/getproxy/123?apikey=test")
	t.Setenv("NVIDIA_ROUTER_XK_VALIDATION_STATUS", "999")

	_, err := LoadFromEnv(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "NVIDIA_ROUTER_XK_VALIDATION_STATUS") {
		t.Fatalf("LoadFromEnv error = %v, want validation status error", err)
	}
}

func TestLoadFromEnvRejectsInvalidPoolDuration(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_XK_UPSTREAM_URL", "https://api.xingkongdaili.com/api/getproxy/123?apikey=test")
	t.Setenv("NVIDIA_ROUTER_XK_COLLECT_INTERVAL", "bogus")

	_, err := LoadFromEnv(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "NVIDIA_ROUTER_XK_COLLECT_INTERVAL") {
		t.Fatalf("LoadFromEnv error = %v, want collect interval error", err)
	}
}

func setBaseConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NVIDIA_ROUTER_MASTER_KEY", validMasterKey())
	t.Setenv("NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD", testInitialAdminPassword)
}

func TestLoadFromEnvLoadsVersionedLegacyMasterKey(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	legacy := validMasterKeyBytes()
	legacy[0] = 99
	t.Setenv("NVIDIA_ROUTER_MASTER_KEY_VERSION", "2")
	t.Setenv("NVIDIA_ROUTER_LEGACY_MASTER_KEY_VERSION", "1")
	t.Setenv("NVIDIA_ROUTER_LEGACY_MASTER_KEY", base64.RawURLEncoding.EncodeToString(legacy[:]))
	cfg, err := LoadFromEnv(LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.MasterKeyVersion != 2 || cfg.LegacyMasterKeyVersion != 1 || cfg.LegacyMasterKey == nil || *cfg.LegacyMasterKey != legacy {
		t.Fatalf("versioned config = %#v", cfg)
	}
}

func TestLoadFromEnvRejectsInvalidMasterKeyVersion(t *testing.T) {
	clearConfigEnv(t)
	setBaseConfigEnv(t)
	t.Setenv("NVIDIA_ROUTER_MASTER_KEY_VERSION", "0")
	if _, err := LoadFromEnv(LoadOptions{}); err == nil || !strings.Contains(err.Error(), "MASTER_KEY_VERSION") {
		t.Fatalf("LoadFromEnv error = %v", err)
	}
}

func TestRequestBodyLimits(t *testing.T) {
	if JSONBodyLimit != 32<<20 || ImageDecodedLimit != 20<<20 || AudioBodyLimit != 25<<20 {
		t.Fatal("request body limits changed")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"NVIDIA_ROUTER_LISTEN_ADDR", "NVIDIA_ROUTER_DATA_DIR", "NVIDIA_ROUTER_TEMP_DIR", "NVIDIA_ROUTER_MASTER_KEY", "NVIDIA_ROUTER_MASTER_KEY_VERSION", "NVIDIA_ROUTER_LEGACY_MASTER_KEY", "NVIDIA_ROUTER_LEGACY_MASTER_KEY_VERSION",
		"NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD", "NVIDIA_ROUTER_ADMIN_SECURE_COOKIE", "NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN",
		"NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS", "NVIDIA_ROUTER_NVIDIA_BASE_URL", "NVIDIA_ROUTER_OPENCODEFREE_BASE_URL", "NVIDIA_ROUTER_OPENCODEFREE_AUTH_KEY",
		"NVIDIA_ROUTER_XK_PROXY_URL", "NVIDIA_ROUTER_XK_PROXY_AUTH_KEY",
		"NVIDIA_ROUTER_XK_UPSTREAM_URL", "NVIDIA_ROUTER_XK_VALIDATION_URL", "NVIDIA_ROUTER_XK_VALIDATION_STATUS",
		"NVIDIA_ROUTER_XK_UPSTREAM_TIMEOUT", "NVIDIA_ROUTER_XK_VALIDATION_TIMEOUT", "NVIDIA_ROUTER_XK_COLLECT_INTERVAL",
		"NVIDIA_ROUTER_XK_PROXY_TTL", "NVIDIA_ROUTER_XK_EXPECTED_QTY", "NVIDIA_ROUTER_XK_CONCURRENCY",
		"NVIDIA_ROUTER_XK_MAX_LATENCY",
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
