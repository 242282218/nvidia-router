package fault

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

const maximumErrorSummaryBytes = 8 << 10

var credentialErrorValues = map[string]struct{}{
	"invalid_api_key":      {},
	"authentication_error": {},
	"account_deactivated":  {},
}

func Classify(response *http.Response, requestErr error, modelsRequest bool, now time.Time) Fault {
	if requestErr != nil {
		return classifyRequestError(requestErr)
	}
	if response == nil {
		return upstreamFault(http.StatusBadGateway, false, "upstream_error", "The upstream request failed.", nil)
	}

	status := response.StatusCode
	summary := readErrorSummary(response.Body)
	switch {
	case status == http.StatusUnauthorized:
		return credentialFault(status)
	case status >= 400 && status <= 499 && summary.invalidCredential():
		return credentialFault(status)
	case status == http.StatusForbidden && modelsRequest:
		return credentialFault(status)
	case status == http.StatusForbidden:
		return Fault{
			HTTPStatus: status, Scope: ScopeModelCredential, Retryable: true, BlockModel: true,
			PublicType: "invalid_request_error", PublicCode: "model_not_available",
			PublicMessage: "The upstream credential cannot access this model.",
		}
	case status == http.StatusTooManyRequests:
		retryAfter, retryAfterValid := ParseRetryAfter(response.Header.Get("Retry-After"), now)
		return Fault{
			HTTPStatus: status, Scope: ScopeTransientCredential, Retryable: true, RetryAfter: retryAfter,
			PublicType: "rate_limit_error", PublicCode: "rate_limit_exceeded",
			PublicMessage: "The upstream service rate limited the request.", retryAfterValid: retryAfterValid,
		}
	case status == 400 || status == 404 || status == 409 || status == 422:
		return requestFault(status)
	case status == 500 || status == 502 || status == 503 || status == 504:
		return upstreamFault(status, true, "upstream_error", "The upstream service is temporarily unavailable.", nil)
	case status >= 500 && status <= 599:
		return upstreamFault(status, false, "upstream_error", "The upstream service rejected the request.", nil)
	case status >= 400 && status <= 499:
		return requestFault(status)
	default:
		return upstreamFault(http.StatusBadGateway, false, "upstream_protocol_error", "The upstream response was unexpected.", nil)
	}
}

func classifyRequestError(err error) Fault {
	if errors.Is(err, context.Canceled) {
		return Fault{
			HTTPStatus: 499, Scope: ScopeRequest, PublicType: "invalid_request_error",
			PublicCode: "request_canceled", PublicMessage: "The request was canceled.", Cause: err,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return upstreamFault(http.StatusGatewayTimeout, true, "upstream_timeout", "The upstream request timed out.", err)
	}
	if isConnectionError(err) {
		return upstreamFault(http.StatusBadGateway, true, "upstream_connection_error", "The upstream connection failed.", err)
	}
	return upstreamFault(http.StatusBadGateway, false, "upstream_error", "The upstream request failed.", err)
}

func credentialFault(status int) Fault {
	return Fault{
		HTTPStatus: status, Scope: ScopeCredential, Retryable: true, DisableKey: true,
		PublicType: "authentication_error", PublicCode: "invalid_api_key",
		PublicMessage: "The upstream credential is invalid.",
	}
}

func requestFault(status int) Fault {
	return Fault{
		HTTPStatus: status, Scope: ScopeRequest, PublicType: "invalid_request_error",
		PublicCode: "upstream_request_rejected", PublicMessage: "The upstream service rejected the request.",
	}
}

func upstreamFault(status int, retryable bool, code, message string, cause error) Fault {
	return Fault{
		HTTPStatus: status, Scope: ScopeUpstreamGlobal, Retryable: retryable,
		PublicType: "server_error", PublicCode: code, PublicMessage: message, Cause: cause,
	}
}

type errorSummary struct {
	code     string
	typeName string
}

func (s errorSummary) invalidCredential() bool {
	for _, value := range []string{s.code, s.typeName} {
		if _, ok := credentialErrorValues[value]; ok {
			return true
		}
	}
	return false
}

func readErrorSummary(body io.Reader) errorSummary {
	if body == nil {
		return errorSummary{}
	}
	payload, err := io.ReadAll(io.LimitReader(body, maximumErrorSummaryBytes+1))
	if err != nil || len(payload) > maximumErrorSummaryBytes {
		return errorSummary{}
	}
	var envelope struct {
		Error struct {
			Code json.RawMessage `json:"code"`
			Type json.RawMessage `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return errorSummary{}
	}
	return errorSummary{
		code:     safeSummaryValue(envelope.Error.Code),
		typeName: safeSummaryValue(envelope.Error.Type),
	}
}

func safeSummaryValue(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' {
			character += 'a' - 'A'
		}
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return ""
		}
	}
	return lowerASCII(value)
}

func lowerASCII(value string) string {
	result := []byte(value)
	for index, character := range result {
		if character >= 'A' && character <= 'Z' {
			result[index] = character + ('a' - 'A')
		}
	}
	return string(result)
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func isConnectionError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE)
}
