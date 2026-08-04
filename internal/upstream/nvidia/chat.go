package nvidia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

const MaxChatResponseBytes = 32 << 20

// ValidatedChatResponse carries the validated body of a 2xx non-streaming chat
// response. The upstream request id is intentionally not captured: no production
// consumer reads it, and pinning it here would only invite dead state. Use
// observability middleware if the request id needs to flow into logs later.
type ValidatedChatResponse struct {
	Body []byte
}

func (c *Client) Chat(
	ctx context.Context,
	snapshot runtimeconfig.Snapshot,
	token string,
	body []byte,
	stream bool,
) (*http.Response, error) {
	response, err := c.do(ctx, snapshot, func(ctx context.Context) (*http.Request, error) {
		request, err := c.descriptor.NewRequest(c.descriptor.Chat, stream, token)
		if err != nil {
			return nil, safeError{"create NVIDIA chat request", err}
		}
		request = request.WithContext(ctx)
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		request.ContentLength = int64(len(body))
		return request, nil
	})
	if err != nil {
		return nil, safeError{"send NVIDIA chat request", err}
	}
	return response, nil
}

func newAttemptTransport(base http.RoundTripper, snapshot runtimeconfig.Snapshot) (http.RoundTripper, *net.Dialer) {
	dialer := &net.Dialer{Timeout: time.Duration(snapshot.ConnectTimeoutMS) * time.Millisecond}
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
		clone := transport.Clone()
		baseDialContext := clone.DialContext
		if baseDialContext == nil {
			clone.DialContext = dialer.DialContext
		} else if snapshot.ConnectTimeoutMS > 0 {
			clone.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				limited, cancel := context.WithTimeout(ctx, dialer.Timeout)
				defer cancel()
				return baseDialContext(limited, network, address)
			}
		}
		clone.ResponseHeaderTimeout = time.Duration(snapshot.FirstByteTimeoutMS) * time.Millisecond
		// Every NVIDIA key targets the same upstream host, so MaxIdleConnsPerHost
		// is the real concurrency ceiling. Cloning http.DefaultTransport inherits
		// its default of 2, which made direct mode rebuild connections under load
		// while the proxy path already used 32. Keep both paths symmetric.
		clone.MaxIdleConns = directMaxIdleConns
		clone.MaxIdleConnsPerHost = directMaxIdleConnsPerHost
		clone.IdleConnTimeout = directIdleConnTimeout
		clone.ForceAttemptHTTP2 = true
		return clone, dialer
	}
	return &attemptRoundTripper{base: base}, dialer
}

// Direct-mode connection pool sizing, mirroring xkproxy's proxy transports so
// the two paths behave the same under concurrency.
const (
	directMaxIdleConns        = 64
	directMaxIdleConnsPerHost = 32
	directIdleConnTimeout     = 60 * time.Second
)

// maxPooledDirectTransports bounds the direct-mode transport cache. The key
// space is small (timeout combinations), but a misconfigured or churning
// settings table must not grow the map without bound.
const maxPooledDirectTransports = 8

// directTransportKey identifies a transport by the timeout settings baked into
// it, so requests with identical settings share keep-alive connections.
type directTransportKey struct {
	connectMS   int
	firstByteMS int
}

// directTransportPool reuses attempt-scoped transports instead of cloning a
// fresh transport (and tearing its connection pool down) on every request.
type directTransportPool struct {
	mu    sync.Mutex
	base  http.RoundTripper
	items map[directTransportKey]*pooledTransport
	clock uint64
}

type pooledTransport struct {
	transport http.RoundTripper
	// lastUsed is a monotonic counter, not a wall-clock timestamp, so LRU
	// ordering is exact even when several requests land in the same nanosecond.
	lastUsed uint64
}

func newDirectTransportPool(base http.RoundTripper) *directTransportPool {
	return &directTransportPool{base: base, items: make(map[directTransportKey]*pooledTransport)}
}

func (p *directTransportPool) Close() {
	p.mu.Lock()
	items := make([]*pooledTransport, 0, len(p.items))
	for key, item := range p.items {
		items = append(items, item)
		delete(p.items, key)
	}
	p.mu.Unlock()
	for _, item := range items {
		closeIdleConnections(item.transport)
	}
}

// Get returns the shared transport for the snapshot's timeout settings,
// creating it on first use. LRU eviction keeps the pool bounded.
func (p *directTransportPool) Get(snapshot runtimeconfig.Snapshot) http.RoundTripper {
	key := directTransportKey{connectMS: snapshot.ConnectTimeoutMS, firstByteMS: snapshot.FirstByteTimeoutMS}
	p.mu.Lock()
	defer p.mu.Unlock()
	if item, ok := p.items[key]; ok {
		p.clock++
		item.lastUsed = p.clock
		return item.transport
	}
	transport, _ := newAttemptTransport(p.base, snapshot)
	p.clock++
	item := &pooledTransport{transport: transport, lastUsed: p.clock}
	p.items[key] = item
	if len(p.items) > maxPooledDirectTransports {
		p.evictLeastRecentlyUsed()
	}
	return transport
}

func (p *directTransportPool) evictLeastRecentlyUsed() {
	var oldestKey directTransportKey
	var oldest uint64
	for key, item := range p.items {
		if oldest == 0 || item.lastUsed < oldest {
			oldestKey = key
			oldest = item.lastUsed
		}
	}
	closeIdleConnections(p.items[oldestKey].transport)
	delete(p.items, oldestKey)
}

func ValidateNonstreamChat(response *http.Response) (ValidatedChatResponse, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxChatResponseBytes+1))
	if err != nil {
		return ValidatedChatResponse{}, safeError{"read NVIDIA chat response", err}
	}
	if len(body) > MaxChatResponseBytes {
		return ValidatedChatResponse{}, protocolError()
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return ValidatedChatResponse{}, protocolError()
	}
	choices, exists := fields["choices"]
	if !exists || !hasValidChatChoices(choices) {
		return ValidatedChatResponse{}, protocolError()
	}

	return ValidatedChatResponse{
		Body: body,
	}, nil
}

func hasValidChatChoices(value json.RawMessage) bool {
	var items []json.RawMessage
	if json.Unmarshal(value, &items) != nil || len(items) == 0 {
		return false
	}
	first := bytes.TrimSpace(items[0])
	return len(first) > 0 && first[0] == '{'
}

func isJSONArray(value json.RawMessage) bool {
	var items []json.RawMessage
	return len(bytes.TrimSpace(value)) > 0 && bytes.TrimSpace(value)[0] == '[' && json.Unmarshal(value, &items) == nil
}

func protocolError() error {
	return safeError{"NVIDIA chat response was malformed", ErrProtocol}
}

type safeError struct {
	message string
	cause   error
}

func (e safeError) Error() string { return e.message }

func (e safeError) Unwrap() error { return e.cause }

type attemptRoundTripper struct {
	base http.RoundTripper
}

func (t *attemptRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(request)
}

func (t *attemptRoundTripper) CloseIdleConnections() {
	closeIdleConnections(t.base)
}

func closeIdleConnections(transport http.RoundTripper) {
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
