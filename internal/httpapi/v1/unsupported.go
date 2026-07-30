package v1

import (
	"net/http"

	"nvidia-router/internal/apierror"
)

// Unsupported responds to any /v1/* endpoint that the router does not implement.
// It never contacts NVIDIA and surfaces no internal details.
var Unsupported http.Handler = http.HandlerFunc(unsupportedHandler)

func unsupportedHandler(writer http.ResponseWriter, _ *http.Request) {
	apierror.Error{
		Status:  http.StatusNotImplemented,
		Type:    "invalid_request_error",
		Code:    "not_implemented",
		Message: "This API endpoint is not implemented.",
	}.Write(writer)
}
