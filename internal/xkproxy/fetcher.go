package xkproxy

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxFetchResponseBytes = 4 << 10

type ErrorReason string

const (
	ReasonFetchFailed     ErrorReason = "fetch_failed"
	ReasonInvalidResponse ErrorReason = "invalid_response"
	ReasonTransportFailed ErrorReason = "transport_failed"
	ReasonManagerClosed   ErrorReason = "manager_closed"
)

type Error struct {
	reason ErrorReason
	cause  error
}

func (e *Error) Error() string { return "upstream proxy unavailable" }

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Reason() ErrorReason { return e.reason }

func newError(reason ErrorReason, cause error) *Error {
	return &Error{reason: reason, cause: cause}
}

func NewTransportError(cause error) *Error {
	return newError(ReasonTransportFailed, cause)
}

type fetcher struct {
	client *http.Client
	apiURL *url.URL
}

func newFetcher(apiURL *url.URL) *fetcher {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Proxy = nil
	copyURL := *apiURL
	return &fetcher{
		client: &http.Client{Transport: transport, Timeout: 5 * time.Second},
		apiURL: &copyURL,
	}
}

func (f *fetcher) Fetch(ctx context.Context) (*url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.apiURL.String(), nil)
	if err != nil {
		return nil, newError(ReasonFetchFailed, err)
	}
	response, err := f.client.Do(request)
	if err != nil {
		return nil, newError(ReasonFetchFailed, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, newError(ReasonInvalidResponse, nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFetchResponseBytes+1))
	if err != nil || len(body) > maxFetchResponseBytes {
		return nil, newError(ReasonInvalidResponse, err)
	}
	line := strings.TrimSpace(string(body))
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return nil, newError(ReasonInvalidResponse, nil)
	}
	address, err := netip.ParseAddrPort(line)
	if err != nil || !address.Addr().IsValid() || address.Addr().Zone() != "" || address.Port() == 0 {
		return nil, newError(ReasonInvalidResponse, err)
	}
	return &url.URL{Scheme: "http", Host: address.String()}, nil
}
