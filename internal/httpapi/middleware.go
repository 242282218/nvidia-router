package httpapi

import (
	"net/http"

	"nvidia-router/internal/accesskey"
)

func DataMiddleware(keys *accesskey.Service, next http.Handler) http.Handler {
	return accesskey.Middleware(keys, next)
}
