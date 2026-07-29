package nvidia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxErrorBodyBytes = 8 << 10

var ErrProtocol = errors.New("NVIDIA models protocol error")

type Client struct {
	httpClient *http.Client
	descriptor Descriptor
}

type ValidationState uint8

const (
	ValidationValid ValidationState = iota
	ValidationInvalidCredential
	ValidationTemporarilyUnavailable
	ValidationIndeterminate
)

type ValidationResult struct {
	State     ValidationState
	Models    []string
	RequestID string
	SafeError string
}

func NewClient(httpClient *http.Client, descriptor Descriptor) (*Client, error) {
	if httpClient == nil {
		return nil, errors.New("new NVIDIA client: HTTP client is required")
	}
	return &Client{httpClient: httpClient, descriptor: descriptor}, nil
}

func (c *Client) Models(ctx context.Context, token string) ([]string, error) {
	response, err := c.modelsRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		discardErrorBody(response.Body)
		return nil, fmt.Errorf("get NVIDIA models: upstream returned HTTP %d", response.StatusCode)
	}
	models, err := parseModels(response.Body)
	if err != nil {
		return nil, fmt.Errorf("get NVIDIA models: %w", err)
	}
	return models, nil
}

func (c *Client) ValidateCredential(ctx context.Context, token string) ValidationResult {
	response, err := c.modelsRequest(ctx, token)
	if err != nil {
		return ValidationResult{State: ValidationTemporarilyUnavailable, SafeError: "NVIDIA models request failed"}
	}
	defer response.Body.Close()
	result := ValidationResult{RequestID: allowedRequestID(response.Header)}
	if response.StatusCode == http.StatusOK {
		models, parseErr := parseModels(response.Body)
		if parseErr != nil {
			result.State = ValidationIndeterminate
			result.SafeError = "NVIDIA models response was malformed"
			return result
		}
		result.State = ValidationValid
		result.Models = models
		return result
	}

	discardErrorBody(response.Body)
	result.SafeError = fmt.Sprintf("NVIDIA models returned HTTP %d", response.StatusCode)
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		result.State = ValidationInvalidCredential
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		result.State = ValidationTemporarilyUnavailable
	default:
		result.State = ValidationIndeterminate
	}
	return result
}

func (c *Client) modelsRequest(ctx context.Context, token string) (*http.Response, error) {
	request, err := c.descriptor.NewRequest(c.descriptor.Models, false, token)
	if err != nil {
		return nil, fmt.Errorf("create NVIDIA models request: %w", err)
	}
	response, err := c.httpClient.Do(request.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("send NVIDIA models request: %w", err)
	}
	return response, nil
}

func discardErrorBody(body io.Reader) {
	_, _ = io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
}

func allowedRequestID(headers http.Header) string {
	for _, header := range []string{"X-Request-Id", "X-Amzn-Requestid"} {
		if requestID := headers.Get(header); requestID != "" {
			return requestID
		}
	}
	return ""
}
