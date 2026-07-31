package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
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
	defaultXKProxyTTL    = 3 * time.Minute
	defaultXKProxyRenew  = 15 * time.Second
)

type Config struct {
	ListenAddress      string
	DataDir            string
	TempDir            string
	MasterKey          [32]byte
	NVIDIABaseURL      *url.URL
	XKProxyAPIURL      *url.URL
	XKProxyTTL         time.Duration
	XKProxyRenewBefore time.Duration
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
	xkProxyURL, xkProxyTTL, xkProxyRenewBefore, err := loadXKProxyConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		ListenAddress:      valueOrDefault("NVIDIA_ROUTER_LISTEN_ADDR", defaultListenAddress),
		DataDir:            valueOrDefault("NVIDIA_ROUTER_DATA_DIR", defaultDataDir),
		TempDir:            valueOrDefault("NVIDIA_ROUTER_TEMP_DIR", defaultTempDir),
		MasterKey:          masterKey,
		NVIDIABaseURL:      nvidiaBaseURL,
		XKProxyAPIURL:      xkProxyURL,
		XKProxyTTL:         xkProxyTTL,
		XKProxyRenewBefore: xkProxyRenewBefore,
	}, nil
}

func loadXKProxyConfig() (*url.URL, time.Duration, time.Duration, error) {
	const urlEnv = "NVIDIA_ROUTER_XK_PROXY_API_URL"
	rawURL := os.Getenv(urlEnv)
	if rawURL == "" {
		return nil, 0, 0, nil
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" {
		return nil, 0, 0, proxyConfigError(urlEnv, "must be an absolute URL without userinfo or fragment")
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, 0, 0, proxyConfigError(urlEnv, "scheme must be http or https")
	}
	values, err := url.ParseQuery(parsedURL.RawQuery)
	if err != nil || len(values["qty"]) != 1 || values.Get("qty") != "1" {
		return nil, 0, 0, proxyConfigError(urlEnv, "query parameter qty must appear exactly once with value 1")
	}

	ttl, err := loadDuration("NVIDIA_ROUTER_XK_PROXY_TTL", defaultXKProxyTTL)
	if err != nil || ttl < 30*time.Second || ttl > 30*time.Minute {
		return nil, 0, 0, proxyConfigError("NVIDIA_ROUTER_XK_PROXY_TTL", "must be between 30s and 30m")
	}
	renewBefore, err := loadDuration("NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE", defaultXKProxyRenew)
	if err != nil || renewBefore <= 0 || renewBefore >= ttl {
		return nil, 0, 0, proxyConfigError("NVIDIA_ROUTER_XK_PROXY_RENEW_BEFORE", "must be greater than 0 and less than TTL")
	}
	return parsedURL, ttl, renewBefore, nil
}

func loadDuration(name string, defaultValue time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue, nil
	}
	return time.ParseDuration(value)
}

func proxyConfigError(name, reason string) error {
	return fmt.Errorf("%s: %s", name, reason)
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
