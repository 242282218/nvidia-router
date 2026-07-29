package fault

import "time"

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

func (fault Fault) Error() string {
	return fault.PublicMessage
}

func (fault Fault) Unwrap() error {
	return fault.Cause
}
