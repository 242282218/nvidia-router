package httpapi

import (
	"net/http"
)

type AdminSecurity interface {
	http.Handler
	RequirePasswordChanged(http.Handler) http.Handler
	RequireManagement(http.Handler) http.Handler
}

func NewRouter(
	health, chat, responses, embeddings, audio, speech, models, unsupported http.Handler,
	security AdminSecurity,
	management, settings, runtimeSummary, frontend http.Handler,
	additionalAdmin ...http.Handler,
) http.Handler {
	root := http.NewServeMux()
	root.Handle("/health", NoStoreMiddleware(health))
	root.Handle("/health/", NoStoreMiddleware(health))
	dataRoutes := NoStoreMiddleware(security.RequirePasswordChanged(newV1Router(chat, responses, embeddings, audio, speech, models, unsupported)))
	root.Handle("/v1", dataRoutes)
	root.Handle("/v1/", dataRoutes)
	root.Handle("/admin/api/auth/", NoStoreMiddleware(security))
	stats := http.NotFoundHandler()
	if len(additionalAdmin) > 0 && additionalAdmin[0] != nil {
		stats = additionalAdmin[0]
	}
	monitoring := http.NotFoundHandler()
	if len(additionalAdmin) > 1 && additionalAdmin[1] != nil {
		monitoring = additionalAdmin[1]
	}
	metrics := http.NotFoundHandler()
	if len(additionalAdmin) > 2 && additionalAdmin[2] != nil {
		metrics = additionalAdmin[2]
	}
	eventStream := http.NotFoundHandler()
	if len(additionalAdmin) > 3 && additionalAdmin[3] != nil {
		eventStream = additionalAdmin[3]
	}
	// Metrics expose key-pool size, cooldown counts, proxy-pool health and
	// request outcomes — enough for an unauthenticated observer on the listener
	// to tell when the pool is exhausted and time an attack. They carry the same
	// management gate as the admin surface (see the deployment design note that
	// ready/admin/metrics stay authenticated).
	root.Handle("/metrics", NoStoreMiddleware(security.RequireManagement(metrics)))
	securedManagement := NoStoreMiddleware(security.RequireManagement(newAdminRouter(management, settings, runtimeSummary, stats, monitoring, eventStream)))
	root.Handle("/admin/api", securedManagement)
	root.Handle("/admin/api/", securedManagement)
	root.Handle("/admin/", frontend)
	root.Handle("/", frontend)
	return root
}

func newAdminRouter(management, settings, runtimeSummary, stats, monitoring, eventStream http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/admin/api/settings", settings)
	mux.Handle("/admin/api/runtime/summary", runtimeSummary)
	mux.Handle("/admin/api/stats", stats)
	// Without this the cost panel's request falls through to the management
	// handler, which has no such route and answers 404.
	mux.Handle("/admin/api/stats/cost", stats)
	mux.Handle("/admin/api/errors", stats)
	mux.Handle("/admin/api/monitoring/", monitoring)
	mux.Handle("/admin/api/events/stream", eventStream)
	mux.Handle("/admin/api", management)
	mux.Handle("/admin/api/", management)
	return mux
}

func newV1Router(chat, responses, embeddings, audio, speech, models, unsupported http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1", unsupported)
	mux.Handle("/v1/chat/completions", chat)
	mux.Handle("/v1/responses", responses)
	mux.Handle("/v1/embeddings", embeddings)
	mux.Handle("/v1/audio/transcriptions", audio)
	mux.Handle("/v1/audio/speech", speech)
	mux.Handle("/v1/models", models)
	// Fallback for any other /v1/* path must come after the concrete routes above.
	mux.Handle("/v1/", unsupported)
	return mux
}
