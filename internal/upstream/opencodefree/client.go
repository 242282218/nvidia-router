// Package opencodefree provides the discovery and read-only probe surface for
// the local OpenCode Free gateway. It is deliberately not a production router
// provider: callers must opt into each operation explicitly.
package opencodefree

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

const maxResponseBytes = 4 << 20

const (
	// proxyAttempts bounds how many pooled exits one request may dial. Three
	// covers a dead exit plus one genuinely flaky retry without turning a broken
	// gateway into a long stall.
	proxyAttempts = 3
	// proxyWaitBudget is how long one attempt waits for the pool to publish a
	// healthy exit before giving up. The collector runs on a ~5s cycle, so this
	// spans roughly two cycles.
	proxyWaitBudget = 10 * time.Second
)

var ErrProtocol = errors.New("OpenCodeFree protocol error")

type Client struct {
	httpClient *http.Client
	baseURL    string
	authKey    string
	proxy      xkproxy.Provider
	// session pins this process to one pooled exit. The gateway sees a single
	// fixed bearer token, so letting every request hop to another IP would make
	// the account look like it is being shared across hosts — a far more likely
	// way to attract attention than a stable exit. A new exit is only picked when
	// the current one fails or its lease expires. The label is random per process
	// so the proxy vendor cannot correlate our sessions across restarts.
	session string
}

func NewClient(httpClient *http.Client, baseURL *url.URL, authKey string) (*Client, error) {
	if httpClient == nil {
		return nil, errors.New("new OpenCodeFree client: HTTP client is required")
	}
	if baseURL == nil || baseURL.Host == "" || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("new OpenCodeFree client: base URL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment")
	}
	session := make([]byte, 16)
	if _, err := rand.Read(session); err != nil {
		return nil, fmt.Errorf("new OpenCodeFree client: generate proxy session label: %w", err)
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL.String(), "/"),
		authKey:    strings.TrimSpace(authKey),
		session:    hex.EncodeToString(session),
	}, nil
}

// WithProxy routes every gateway call through the built-in proxy pool. Without
// it the client dials the gateway directly.
func (c *Client) WithProxy(provider xkproxy.Provider) *Client {
	if c == nil || provider == nil {
		return c
	}
	c.proxy = provider
	return c
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	response, err := c.do(ctx, runtimeconfig.Snapshot{}, http.MethodGet, "/models", nil, false)
	if err != nil {
		return nil, fmt.Errorf("request OpenCodeFree models: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenCodeFree models returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read OpenCodeFree models: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("%w: model response exceeds %d bytes", ErrProtocol, maxResponseBytes)
	}
	return parseModels(body)
}

func (c *Client) Chat(ctx context.Context, snapshot runtimeconfig.Snapshot, body []byte, stream bool) (*http.Response, error) {
	response, err := c.do(ctx, snapshot, http.MethodPost, "/chat/completions", body, stream)
	if err != nil {
		return nil, fmt.Errorf("request OpenCodeFree chat: %w", err)
	}
	return response, nil
}

// do sends one gateway call, through the proxy pool when one is wired. A pooled
// attempt that never produced a response retires its exit and dials another, so
// a dead or throttled IP costs a retry instead of the whole request. An attempt
// that already reached the gateway is never replayed: the gateway may have
// accepted it and a second copy would double the call.
func (c *Client) do(ctx context.Context, snapshot runtimeconfig.Snapshot, method, path string, body []byte, stream bool) (*http.Response, error) {
	if c.proxy == nil || !c.proxy.Configured() {
		request, err := c.newRequest(ctx, method, path, body, stream)
		if err != nil {
			return nil, err
		}
		return c.httpClient.Do(request)
	}
	if !c.proxy.Enabled() {
		return nil, xkproxy.NewTransportError(errors.New("proxy is disabled"))
	}
	var lastErr error
	for attempt := 0; attempt < proxyAttempts; attempt++ {
		request, err := c.newRequest(ctx, method, path, body, stream)
		if err != nil {
			return nil, err
		}
		response, wrote, err := c.attemptThroughProxy(ctx, snapshot, request)
		if response != nil || err == nil {
			return response, err
		}
		lastErr = err
		if wrote || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) attemptThroughProxy(ctx context.Context, snapshot runtimeconfig.Snapshot, request *http.Request) (*http.Response, bool, error) {
	handle, err := xkproxy.AcquireWithWait(ctx, c.proxy, snapshot, c.session, proxyWaitBudget)
	if err != nil {
		return nil, false, err
	}
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				wrote.Store(true)
			}
		},
	}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	httpClient := *c.httpClient
	httpClient.Transport = handle.Transport()
	started := time.Now()
	response, err := httpClient.Do(request)
	if response != nil {
		switch {
		case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
			// CONNECT plus a 2xx header proves this exit carries gateway traffic:
			// feed first-byte quality and clear any isolation window.
			handle.ReportRequestLatency(time.Since(started))
			handle.ReportLatency(0)
		case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError:
			// Throttling and server failures may be specific to this exit, so let
			// the pool isolate it rather than keep ranking it as healthy.
			handle.ReportHTTPFailure(response.StatusCode)
		}
		return response, wrote.Load(), err
	}
	// Nothing came back at all. Only a failure before the request left the wire
	// points at the exit; a gateway that hangs up mid-flight would otherwise let
	// one bad gateway retire every healthy exit in the pool.
	if err == nil {
		err = errors.New("OpenCodeFree proxy transport returned no response")
	}
	if wrote.Load() || ctx.Err() != nil {
		return nil, wrote.Load(), err
	}
	handle.Retire(xkproxy.RetireReasonTransportError)
	return nil, false, err
}

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte, stream bool) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create OpenCodeFree request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.ContentLength = int64(len(body))
	}
	request.Header.Set("x-opencode-client", "desktop")
	if c.authKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.authKey)
	}
	return request, nil
}

type modelEnvelope struct {
	Data []modelRecord `json:"data"`
}

type modelRecord struct {
	ID string `json:"id"`
}

func parseModels(body []byte) ([]string, error) {
	var envelope modelEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return nil, fmt.Errorf("%w: decode model list", ErrProtocol)
	}
	free := make([]string, 0, len(envelope.Data))
	other := make([]string, 0, len(envelope.Data))
	seen := make(map[string]struct{}, len(envelope.Data))
	for _, record := range envelope.Data {
		modelID := strings.TrimSpace(record.ID)
		if modelID == "" {
			return nil, fmt.Errorf("%w: model id is empty", ErrProtocol)
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}
		if strings.HasSuffix(strings.ToLower(modelID), "-free") {
			free = append(free, modelID)
		} else {
			other = append(other, modelID)
		}
	}
	if len(free)+len(other) == 0 {
		return nil, fmt.Errorf("%w: model list is empty", ErrProtocol)
	}
	return append(free, other...), nil
}
