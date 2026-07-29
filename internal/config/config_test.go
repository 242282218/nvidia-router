package config

import (
	"encoding/base64"
	"strings"
	"testing"
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
