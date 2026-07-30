package httpapi

import (
	"net/http"

	v1 "nvidia-router/internal/httpapi/v1"
)

func NewRouter(health, chat, responses, embeddings, audio, speech, models http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health/", health)
	mux.Handle("/v1/chat/completions", chat)
	mux.Handle("/v1/responses", responses)
	mux.Handle("/v1/embeddings", embeddings)
	mux.Handle("/v1/audio/transcriptions", audio)
	mux.Handle("/v1/audio/speech", speech)
	mux.Handle("/v1/models", models)
	// Fallback for any other /v1/* path must come after the concrete routes above.
	mux.Handle("/v1/", v1.Unsupported)
	mux.HandleFunc("/admin/api/", http.NotFound)
	mux.HandleFunc("/admin/", frontendPlaceholder)
	mux.HandleFunc("/", frontendPlaceholder)
	return mux
}

func frontendPlaceholder(writer http.ResponseWriter, request *http.Request) {
	http.NotFound(writer, request)
}
