package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
)

const (
	JSONBodyLimit     = 32 << 20
	ImageDecodedLimit = 20 << 20
	AudioBodyLimit    = 25 << 20
)

const (
	defaultListenAddress = "0.0.0.0:3756"
	defaultDataDir       = "/data"
	defaultTempDir       = "/tmp"
	defaultNVIDIABaseURL = "https://integrate.api.nvidia.com"
)

type Config struct {
	ListenAddress string
	DataDir       string
	TempDir       string
	MasterKey     [32]byte
	NVIDIABaseURL *url.URL
}

type LoadOptions struct {
	AllowInsecureTestUpstream bool
}

func LoadFromEnv(opts LoadOptions) (Config, error) {
	masterKey, err := loadMasterKey(os.Getenv("NVIDIA_ROUTER_MASTER_KEY"))
	if err != nil {
		return Config{}, fmt.Errorf("load master key: %w", err)
	}

	nvidiaBaseURL, err := loadNVIDIABaseURL(
		valueOrDefault("NVIDIA_ROUTER_NVIDIA_BASE_URL", defaultNVIDIABaseURL),
		opts.AllowInsecureTestUpstream,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load NVIDIA base URL: %w", err)
	}

	return Config{
		ListenAddress: valueOrDefault("NVIDIA_ROUTER_LISTEN_ADDR", defaultListenAddress),
		DataDir:       valueOrDefault("NVIDIA_ROUTER_DATA_DIR", defaultDataDir),
		TempDir:       valueOrDefault("NVIDIA_ROUTER_TEMP_DIR", defaultTempDir),
		MasterKey:     masterKey,
		NVIDIABaseURL: nvidiaBaseURL,
	}, nil
}

func valueOrDefault(name, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return defaultValue
}

func loadMasterKey(encoded string) ([32]byte, error) {
	var key [32]byte
	if encoded == "" {
		return key, errors.New("NVIDIA_ROUTER_MASTER_KEY is required")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return key, fmt.Errorf("decode NVIDIA_ROUTER_MASTER_KEY as Raw URL Base64: %w", err)
	}
	if len(decoded) != len(key) {
		return key, fmt.Errorf("NVIDIA_ROUTER_MASTER_KEY must decode to 32 bytes: got %d", len(decoded))
	}
	copy(key[:], decoded)
	return key, nil
}

func loadNVIDIABaseURL(rawURL string, allowInsecure bool) (*url.URL, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse NVIDIA_ROUTER_NVIDIA_BASE_URL: %w", err)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("validate NVIDIA_ROUTER_NVIDIA_BASE_URL: %w", errors.New("host is required"))
	}
	if parsedURL.Scheme != "https" && (!allowInsecure || parsedURL.Scheme != "http") {
		return nil, fmt.Errorf("validate NVIDIA_ROUTER_NVIDIA_BASE_URL: %w", errors.New("HTTPS is required"))
	}
	return parsedURL, nil
}
