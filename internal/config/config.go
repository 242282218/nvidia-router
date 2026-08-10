package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	ListenAddress          string
	DataDir                string
	TempDir                string
	MasterKey              [32]byte
	MasterKeyVersion       int
	LegacyMasterKey        *[32]byte
	LegacyMasterKeyVersion int
	InitialAdminPassword   string
	AdminSecureCookie      bool
	AdminExternalOrigin    *url.URL
	TrustedProxyCIDRs      []*net.IPNet
	NVIDIABaseURL          *url.URL
	XKProxyURL             *url.URL
	XKProxyAuthKey         string
}

type LoadOptions struct {
	AllowInsecureTestUpstream bool
}

func LoadFromEnv(opts LoadOptions) (Config, error) {
	masterKey, err := loadMasterKey(os.Getenv("NVIDIA_ROUTER_MASTER_KEY"))
	if err != nil {
		return Config{}, fmt.Errorf("load master key: %w", err)
	}
	masterKeyVersion, err := loadPositiveInt("NVIDIA_ROUTER_MASTER_KEY_VERSION", 1)
	if err != nil {
		return Config{}, err
	}
	legacyMasterKey, err := loadOptionalMasterKey(os.Getenv("NVIDIA_ROUTER_LEGACY_MASTER_KEY"))
	if err != nil {
		return Config{}, fmt.Errorf("load legacy master key: %w", err)
	}
	legacyMasterKeyVersion, err := loadPositiveInt("NVIDIA_ROUTER_LEGACY_MASTER_KEY_VERSION", 1)
	if err != nil {
		return Config{}, err
	}
	if legacyMasterKey != nil && legacyMasterKeyVersion == masterKeyVersion {
		return Config{}, errors.New("NVIDIA_ROUTER_LEGACY_MASTER_KEY_VERSION must differ from NVIDIA_ROUTER_MASTER_KEY_VERSION")
	}
	initialAdminPassword := os.Getenv("NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD")
	if initialAdminPassword == "" {
		return Config{}, errors.New("NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD is required")
	}
	secureCookie, err := loadBool("NVIDIA_ROUTER_ADMIN_SECURE_COOKIE", false)
	if err != nil {
		return Config{}, err
	}
	if err := validateInitialAdminPassword(initialAdminPassword); err != nil {
		return Config{}, fmt.Errorf("NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD: %w", err)
	}
	externalOrigin, trustedProxyCIDRs, err := loadOriginConfig()
	if err != nil {
		return Config{}, err
	}
	if externalOrigin != nil && externalOrigin.Scheme == "https" && !secureCookie {
		return Config{}, errors.New("NVIDIA_ROUTER_ADMIN_SECURE_COOKIE must be true for an HTTPS external origin")
	}

	nvidiaBaseURL, err := loadNVIDIABaseURL(
		valueOrDefault("NVIDIA_ROUTER_NVIDIA_BASE_URL", defaultNVIDIABaseURL),
		opts.AllowInsecureTestUpstream,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load NVIDIA base URL: %w", err)
	}
	xkProxyURL, xkProxyAuthKey, err := loadXKProxyConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		ListenAddress:          valueOrDefault("NVIDIA_ROUTER_LISTEN_ADDR", defaultListenAddress),
		DataDir:                valueOrDefault("NVIDIA_ROUTER_DATA_DIR", defaultDataDir),
		TempDir:                valueOrDefault("NVIDIA_ROUTER_TEMP_DIR", defaultTempDir),
		MasterKey:              masterKey,
		MasterKeyVersion:       masterKeyVersion,
		LegacyMasterKey:        legacyMasterKey,
		LegacyMasterKeyVersion: legacyMasterKeyVersion,
		InitialAdminPassword:   initialAdminPassword,
		AdminSecureCookie:      secureCookie,
		AdminExternalOrigin:    externalOrigin,
		TrustedProxyCIDRs:      trustedProxyCIDRs,
		NVIDIABaseURL:          nvidiaBaseURL,
		XKProxyURL:             xkProxyURL,
		XKProxyAuthKey:         xkProxyAuthKey,
	}, nil
}

func loadBool(name string, defaultValue bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: must be true or false", name)
	}
	return parsed, nil
}

func loadOriginConfig() (*url.URL, []*net.IPNet, error) {
	rawOrigin := os.Getenv("NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN")
	var externalOrigin *url.URL
	if rawOrigin != "" {
		parsed, err := url.Parse(rawOrigin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, nil, errors.New("NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN: must be an absolute http or https origin without path, query, fragment, or userinfo")
		}
		externalOrigin = parsed
	}

	rawProxies := strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS"))
	if rawProxies == "" {
		return externalOrigin, nil, nil
	}
	var trusted []*net.IPNet
	for _, value := range strings.Split(rawProxies, ",") {
		value = strings.TrimSpace(value)
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, nil, fmt.Errorf("NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS: invalid CIDR %q", value)
		}
		trusted = append(trusted, network)
	}
	return externalOrigin, trusted, nil
}

func loadXKProxyConfig() (*url.URL, string, error) {
	const (
		urlEnv  = "NVIDIA_ROUTER_XK_PROXY_URL"
		authEnv = "NVIDIA_ROUTER_XK_PROXY_AUTH_KEY"
	)
	rawURL := strings.TrimSpace(os.Getenv(urlEnv))
	authKey := os.Getenv(authEnv)
	if rawURL == "" {
		if authKey != "" {
			return nil, "", proxyConfigError(authEnv, "requires NVIDIA_ROUTER_XK_PROXY_URL")
		}
		return nil, "", nil
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.ForceQuery || parsedURL.Fragment != "" || (parsedURL.Path != "" && parsedURL.Path != "/") {
		return nil, "", proxyConfigError(urlEnv, "must be an absolute HTTP or HTTPS proxy URL without credentials, query, or fragment")
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, "", proxyConfigError(urlEnv, "scheme must be http or https")
	}
	if strings.TrimSpace(authKey) == "" {
		return nil, "", proxyConfigError(authEnv, "is required when NVIDIA_ROUTER_XK_PROXY_URL is set")
	}
	return parsedURL, authKey, nil
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

func loadPositiveInt(name string, defaultValue int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s: must be a positive integer", name)
	}
	return parsed, nil
}

// LoadMasterKey decodes the same 32-byte Raw URL Base64 format used by the
// runtime configuration. It is exposed for offline commands that must avoid
// duplicating secret parsing rules.
func LoadMasterKey(encoded string) ([32]byte, error) {
	return loadMasterKey(encoded)
}

func loadOptionalMasterKey(encoded string) (*[32]byte, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	key, err := loadMasterKey(encoded)
	if err != nil {
		return nil, err
	}
	return &key, nil
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
	// The decoded slice carries the secret bytes in cleartext; match the Zero
	// hygiene other crypto paths use so the temporary buffer is not left in
	// heap waiting for the GC to miss it.
	defer clear(decoded)
	if len(decoded) != len(key) {
		return key, fmt.Errorf("NVIDIA_ROUTER_MASTER_KEY must decode to 32 bytes: got %d", len(decoded))
	}
	copy(key[:], decoded)
	return key, nil
}

func validateInitialAdminPassword(password string) error {
	if len([]rune(password)) < 12 {
		return errors.New("must be at least 12 characters")
	}
	if password == "admin" {
		return errors.New("must not equal admin")
	}
	return nil
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
