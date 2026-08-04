package app

import (
	"log/slog"
	"net/http"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/httpapi"
	"nvidia-router/internal/observability"
)

func observedHandler(recorder observability.RequestRecorder, source clock.Clock, logger *slog.Logger, next http.Handler) http.Handler {
	return observability.HTTPMiddleware(recorder, source, logger, httpapi.RecoverMiddleware(logger, next))
}
