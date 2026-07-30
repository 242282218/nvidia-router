package app

import (
	"net/http"
	"strings"

	"nvidia-router/internal/apierror"
)

func shutdownMiddleware(shutting func() bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if shutting != nil && shutting() && isShutdownAPIPath(request.URL.Path) {
			apierror.Error{
				Status: http.StatusServiceUnavailable, Type: "server_error", Code: "server_shutting_down",
				Message: "The server is shutting down.",
			}.Write(writer)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func isShutdownAPIPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/") || path == "/admin/api" || strings.HasPrefix(path, "/admin/api/")
}
