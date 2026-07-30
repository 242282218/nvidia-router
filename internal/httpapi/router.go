package httpapi

import (
	"net/http"

	v1 "nvidia-router/internal/httpapi/v1"
)

type AdminSecurity interface {
	http.Handler
	RequirePasswordChanged(http.Handler) http.Handler
	RequireManagement(http.Handler) http.Handler
}

func NewRouter(health, chat, responses, embeddings, audio, speech, models http.Handler, security AdminSecurity) http.Handler {
	root := http.NewServeMux()
	root.Handle("/health/", health)
	dataRoutes := security.RequirePasswordChanged(newV1Router(chat, responses, embeddings, audio, speech, models))
	root.Handle("/v1", dataRoutes)
	root.Handle("/v1/", dataRoutes)
	root.Handle("/admin/api/auth/", security)
	managementFallback := security.RequireManagement(http.NotFoundHandler())
	root.Handle("/admin/api", managementFallback)
	root.Handle("/admin/api/", managementFallback)
	root.HandleFunc("/admin/", frontendPlaceholder)
	root.HandleFunc("/", frontendPlaceholder)
	return root
}

func newV1Router(chat, responses, embeddings, audio, speech, models http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1", v1.Unsupported)
	mux.Handle("/v1/chat/completions", chat)
	mux.Handle("/v1/responses", responses)
	mux.Handle("/v1/embeddings", embeddings)
	mux.Handle("/v1/audio/transcriptions", audio)
	mux.Handle("/v1/audio/speech", speech)
	mux.Handle("/v1/models", models)
	// Fallback for any other /v1/* path must come after the concrete routes above.
	mux.Handle("/v1/", v1.Unsupported)
	return mux
}

func frontendPlaceholder(writer http.ResponseWriter, request *http.Request) {
	http.NotFound(writer, request)
}
