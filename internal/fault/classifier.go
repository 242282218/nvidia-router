package fault

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const (
	maximumErrorSummaryBytes = 8 << 10
	// maximumPublicMessageBytes bounds how much of the upstream message is
	// forwarded to clients; a verbose upstream error must not balloon the fault.
	maximumPublicMessageBytes = 1 << 10
)

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
		return credentialFault(status, true)
	case status == http.StatusForbidden && summary.invalidCredential():
		return credentialFault(status, true)
	case status == http.StatusForbidden && modelsRequest:
		return credentialFault(status, true)
	case status == http.StatusForbidden:
		return Fault{
			HTTPStatus: status, Scope: ScopeModelCredential, Retryable: true, BlockModel: true,
			PublicType: "invalid_request_error", PublicCode: "model_not_available",
			PublicMessage: "The upstream credential cannot access this model.",
		}
	// A non-401/403 response whose body names a credential error code is
	// ambiguous: the upstream may be reusing a generic error string. Recognise
	// it as a request fault (message suppressed, not retried) but do NOT disable
	// the key — DisableKey has no automatic recovery, and a one-off
	// misclassified response would otherwise take a healthy key offline forever.
	case status >= 400 && status <= 499 && summary.invalidCredential():
		return credentialFault(status, false)
	case status == http.StatusTooManyRequests:
		retryAfter, retryAfterValid := ParseRetryAfter(response.Header.Get("Retry-After"), now)
		return Fault{
			HTTPStatus: status, Scope: ScopeTransientCredential, Retryable: true, RetryAfter: retryAfter,
			PublicType: "rate_limit_error", PublicCode: "rate_limit_exceeded",
			PublicMessage: "The upstream service rate limited the request.", retryAfterValid: retryAfterValid,
		}
	case status == 400 || status == 404 || status == 409 || status == 422:
		return requestFault(status, summary.message)
	case status == 500 || status == 502 || status == 503 || status == 504 || status == 529:
		// Carry an upstream Retry-After into the key cooldown so a 5xx that names
		// a retry window is honoured instead of the fixed transient 15s. attempt
		// backoff already respects RetryAfter on 429; this extends it to server
		// faults the operator widened into the failover set (audit P2-3).
		retryAfter, retryAfterValid := ParseRetryAfter(response.Header.Get("Retry-After"), now)
		fault := upstreamFault(status, true, "upstream_error", "The upstream service is temporarily unavailable.", nil)
		if retryAfter > 0 {
			fault.RetryAfter = retryAfter
			fault.retryAfterValid = retryAfterValid
		}
		return fault
	case status >= 500 && status <= 599:
		return upstreamFault(status, false, "upstream_error", "The upstream service rejected the request.", nil)
	case status >= 400 && status <= 499:
		return requestFault(status, summary.message)
	default:
		return upstreamFault(http.StatusBadGateway, false, "upstream_protocol_error", "The upstream response was unexpected.", nil)
	}
}

func classifyRequestError(err error) Fault {
	var classified Fault
	if errors.As(err, &classified) {
		return classified
	}
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

func credentialFault(status int, disableKey bool) Fault {
	// A credential fault is per-key: the token itself is bad, so replaying the
	// same request on another key just burns another key's quota on the same
	// doomed auth (audit R5). The key is disabled (DisableKey) and cooled down
	// instead of being retried across the pool.
	fault := Fault{
		HTTPStatus: status, Scope: ScopeCredential, Retryable: false, DisableKey: disableKey,
		PublicType: "authentication_error", PublicCode: "invalid_api_key",
		PublicMessage: "The upstream credential is invalid.",
	}
	if !disableKey {
		// A non-401/403 body that names a credential error code is treated as a
		// non-retryable request fault: the message is suppressed (a body that
		// claims an auth error may carry credential-adjacent detail), but the key
		// is not disabled — the status is not authoritative, so disabling on it
		// would take a healthy key offline with no automatic recovery.
		fault.Scope = ScopeRequest
		fault.PublicCode = "upstream_request_rejected"
		fault.PublicMessage = "The upstream service rejected the request."
	}
	return fault
}

func requestFault(status int, upstreamMessage string) Fault {
	message := upstreamMessage
	if message == "" {
		message = "The upstream service rejected the request."
	}
	return Fault{
		HTTPStatus: status, Scope: ScopeRequest, PublicType: "invalid_request_error",
		PublicCode: "upstream_request_rejected", PublicMessage: message,
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
	// message is the upstream's own description of why the request was
	// rejected. Only surfaced for request faults (4xx describing the client's
	// own request); it is never used for credential/rate-limit/server faults
	// whose bodies may carry internal or credential-adjacent detail.
	message string
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
	payload, err := io.ReadAll(io.LimitReader(body, maximumErrorSummaryBytes))
	if err != nil {
		return errorSummary{}
	}
	var envelope struct {
		Error struct {
			Code    json.RawMessage `json:"code"`
			Type    json.RawMessage `json:"type"`
			Message string          `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return errorSummary{}
	}
	return errorSummary{
		code:     safeSummaryValue(envelope.Error.Code),
		typeName: safeSummaryValue(envelope.Error.Type),
		message:  safeMessageValue(envelope.Error.Message),
	}
}

// safeMessageValue trims and caps the upstream error message so a verbose
// body cannot balloon a fault nor be forwarded in full to clients.
func safeMessageValue(message string) string {
	trimmed := strings.TrimSpace(message)
	if len(trimmed) > maximumPublicMessageBytes {
		return trimmed[:maximumPublicMessageBytes]
	}
	return trimmed
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
