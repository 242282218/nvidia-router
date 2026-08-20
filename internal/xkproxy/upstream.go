package xkproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxUpstreamResponseBytes = 64 << 10

var providerStatusPattern = regexp.MustCompile(`(?i)(?:code|status|error)\D{0,4}(201|202|203|204|205|206|207|208|211|302|303|403|406|407|430|432|436|506)\b`)

var providerErrors = map[string]string{
	"201": "request format invalid",
	"202": "secret validation failed",
	"203": "whitelist limit reached",
	"204": "package expired",
	"205": "package quota reached",
	"206": "no proxy available",
	"207": "machine outside service range",
	"208": "quota exhausted",
	"211": "whitelist required",
	"302": "API order expired",
	"303": "quantity exceeds maximum",
	"403": "request frequency too high",
	"406": "extract frequency too high",
	"407": "proxy expired or whitelist required",
	"430": "domestic whitelist required",
	"432": "account error",
	"436": "proxy unauthorized or expired",
	"506": "quantity exceeds maximum",
}

var rateLimitCodes = map[string]struct{}{
	"403": {},
	"406": {},
}

var accountCodes = map[string]struct{}{
	"204": {},
	"205": {},
	"208": {},
	"211": {},
	"302": {},
	"430": {},
	"432": {},
}

// ProviderError carries a code the provider returned in its response body
type ProviderError struct {
	Code    string
	Message string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider code %s: %s", e.Code, e.Message)
}

// TransportError marks a failure before the provider could answer
type TransportError struct {
	Op  string
	Err error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *TransportError) Unwrap() error {
	return e.Err
}

type UpstreamClient struct {
	URL     string
	timeout time.Duration

	mu            sync.Mutex
	client        *http.Client
	unsafeForTest bool
}

func NewUpstreamClient(rawURL string, timeout time.Duration, expectedQty ...int) *UpstreamClient {
	quantity := 2
	if len(expectedQty) > 0 && expectedQty[0] > 0 {
		quantity = expectedQty[0]
	}
	rawURL = upstreamURLWithQuantity(rawURL, quantity)
	return &UpstreamClient{
		URL:     rawURL,
		timeout: timeout,
	}
}

// NewUpstreamClientForTest creates a client that allows connections to private IPs
// for testing purposes only. NEVER use this in production.
func NewUpstreamClientForTest(rawURL string, timeout time.Duration, expectedQty ...int) *UpstreamClient {
	c := NewUpstreamClient(rawURL, timeout, expectedQty...)
	c.unsafeForTest = true
	return c
}

func upstreamURLWithQuantity(rawURL string, quantity int) string {
	if quantity <= 0 {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set("qty", fmt.Sprintf("%d", quantity))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (c *UpstreamClient) ID() string {
	// The configured XApi URL can contain apikey/sign credentials. Keep the
	// provider identity opaque so callers cannot accidentally put secrets in
	// logs, metrics, or audit records.
	return "xingkong-xapi"
}

func (c *UpstreamClient) httpClient() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		c.client = &http.Client{
			Transport: &http.Transport{
				DialContext:         safeDialContext(c.timeout, c.unsafeForTest),
				TLSHandshakeTimeout: c.timeout,
				MaxIdleConns:        2,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
			},
			Timeout: c.timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return c.client
}

// safeDialContext returns a DialContext that rejects private/loopback/metadata IPs
// at connection time, mitigating DNS rebinding attacks where a public hostname
// resolves to a private IP at dial time.
func safeDialContext(timeout time.Duration, allowPrivateForTest bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if allowPrivateForTest {
			return dialer.DialContext(ctx, network, addr)
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("parse dial address: %w", err)
		}

		// Resolve the hostname to IP addresses
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}

		// Check all resolved IPs - reject if any are private/loopback/metadata
		for _, ipAddr := range ips {
			if isPrivateOrMetadataIP(ipAddr.IP) {
				return nil, fmt.Errorf("dial %s: resolved to private/loopback/metadata IP %s", host, ipAddr.IP)
			}
		}

		// All IPs are safe, proceed with dial
		return dialer.DialContext(ctx, network, addr)
	}
}

// isPrivateOrMetadataIP checks if an IP is private, loopback, link-local, or metadata service
func isPrivateOrMetadataIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Standard checks from net package
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Go 1.17+ has IsPrivate, but also check explicit ranges for robustness
	if ip.IsPrivate() {
		return true
	}

	// Explicit metadata service IPs (AWS, GCP, Azure, etc.)
	metadataIPs := []string{
		"169.254.169.254/32", // AWS/Azure/GCP metadata
		"fd00:ec2::254/128",  // AWS IPv6 metadata
	}

	for _, cidrStr := range metadataIPs {
		_, cidr, _ := net.ParseCIDR(cidrStr)
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}

	// Explicit private ranges (redundant with IsPrivate in Go 1.17+, but defensive)
	privateCIDRs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"fc00::/7",
		"fe80::/10",
		"::1/128",
	}

	for _, cidrStr := range privateCIDRs {
		_, cidr, _ := net.ParseCIDR(cidrStr)
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}

	return false
}

func (c *UpstreamClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return
	}
	if transport, ok := c.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func (c *UpstreamClient) Fetch(ctx context.Context) ([]Proxy, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, time.Time{}, &TransportError{Op: "create upstream request", Err: err}
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, time.Time{}, &TransportError{Op: "request upstream", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamResponseBytes+1))
	if err != nil {
		return nil, time.Time{}, &TransportError{Op: "read upstream response", Err: err}
	}
	if len(body) > maxUpstreamResponseBytes {
		return nil, time.Time{}, &TransportError{
			Op:  "read upstream response",
			Err: fmt.Errorf("response exceeds %d bytes", maxUpstreamResponseBytes),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, &TransportError{
			Op:  "upstream HTTP status",
			Err: fmt.Errorf("status %d", resp.StatusCode),
		}
	}

	fetchedAt := time.Now()
	proxies, err := ParseProxies(string(body))
	if err != nil {
		return nil, fetchedAt, err
	}
	return proxies, fetchedAt, nil
}

func ParseProxies(raw string) ([]Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty upstream response")
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\r' || r == '\n'
	})
	seen := make(map[string]struct{}, len(parts))
	proxies := make([]Proxy, 0, len(parts))
	for _, part := range parts {
		address := strings.TrimSpace(part)
		if address == "" {
			continue
		}
		proxy, err := ParseProxy(address)
		if err != nil {
			continue
		}
		if _, ok := seen[proxy.Key()]; ok {
			continue
		}
		seen[proxy.Key()] = struct{}{}
		proxies = append(proxies, proxy)
	}
	if len(proxies) > 0 {
		return proxies, nil
	}

	if code := providerCode(raw); code != "" {
		message := providerErrors[code]
		if message == "" {
			message = "unknown provider error"
		}
		return nil, &ProviderError{Code: code, Message: message}
	}
	return nil, fmt.Errorf("no valid proxy in upstream response")
}

func providerCode(raw string) string {
	lines := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\r' || r == '\n'
	})
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := ParseProxy(line); err == nil {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			code := strings.Trim(fields[0], "[]{}(),;:\"")
			if providerErrors[code] != "" {
				return code
			}
		}
		match := providerStatusPattern.FindStringSubmatch(line)
		if len(match) == 2 {
			return match[1]
		}
	}
	return ""
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Code
	}
	return ""
}

func ShouldBackOff(code string) bool {
	if code == "" {
		return false
	}
	if _, ok := rateLimitCodes[code]; ok {
		return true
	}
	_, ok := accountCodes[code]
	return ok
}

func FatalCode(code string) bool {
	_, ok := accountCodes[code]
	return ok
}
