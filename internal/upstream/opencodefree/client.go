// Package opencodefree provides the discovery and read-only probe surface for
// the local OpenCode Free gateway. It is deliberately not a production router
// provider: callers must opt into each operation explicitly.
package opencodefree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"nvidia-router/internal/runtimeconfig"
)

const maxResponseBytes = 4 << 20

var ErrProtocol = errors.New("OpenCodeFree protocol error")

type Client struct {
	httpClient *http.Client
	baseURL    string
	authKey    string
}

func NewClient(httpClient *http.Client, baseURL *url.URL, authKey string) (*Client, error) {
	if httpClient == nil {
		return nil, errors.New("new OpenCodeFree client: HTTP client is required")
	}
	if baseURL == nil || baseURL.Host == "" || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("new OpenCodeFree client: base URL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment")
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL.String(), "/"),
		authKey:    strings.TrimSpace(authKey),
	}, nil
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	request, err := c.newRequest(ctx, http.MethodGet, "/models", nil, false)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
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

func (c *Client) Chat(ctx context.Context, _ runtimeconfig.Snapshot, body []byte, stream bool) (*http.Response, error) {
	request, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", body, stream)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request OpenCodeFree chat: %w", err)
	}
	return response, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte, stream bool) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, strings.NewReader(string(body)))
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
