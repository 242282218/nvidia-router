package fault

import (
	"net/http"
	"time"
)

type Scope uint8

const (
	ScopeRequest Scope = iota
	ScopeCredential
	ScopeModelCredential
	ScopeTransientCredential
	ScopeUpstreamGlobal
)

type Fault struct {
	HTTPStatus      int
	Scope           Scope
	Retryable       bool
	RetryAfter      time.Duration
	DisableKey      bool
	BlockModel      bool
	PublicType      string
	PublicCode      string
	PublicMessage   string
	Cause           error
	retryAfterValid bool
}

func New(httpStatus int, scope Scope, publicType, publicCode, publicMessage string, cause error) Fault {
	return Fault{
		HTTPStatus:    httpStatus,
		Scope:         scope,
		PublicType:    publicType,
		PublicCode:    publicCode,
		PublicMessage: publicMessage,
		Cause:         cause,
	}
}

func Protocol(cause error) Fault {
	return Fault{
		HTTPStatus: http.StatusBadGateway, Scope: ScopeUpstreamGlobal, Retryable: true,
		PublicType: "server_error", PublicCode: "upstream_protocol_error",
		PublicMessage: "The upstream response was malformed.", Cause: cause,
	}
}

func EmptyResponse(cause error) Fault {
	return Fault{
		HTTPStatus: http.StatusTooManyRequests, Scope: ScopeTransientCredential, Retryable: true,
		RetryAfter: time.Second, PublicType: "rate_limit_error", PublicCode: "upstream_empty_response",
		PublicMessage: "The upstream service returned no usable output. Please retry.", Cause: cause,
	}
}

func (fault Fault) Error() string {
	return fault.PublicMessage
}

func (fault Fault) Unwrap() error {
	return fault.Cause
}
