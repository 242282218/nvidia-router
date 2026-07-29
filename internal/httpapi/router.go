package httpapi

import "net/http"

func NewRouter(health http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health/", health)
	mux.HandleFunc("/v1/", http.NotFound)
	mux.HandleFunc("/admin/api/", http.NotFound)
	mux.HandleFunc("/admin/", frontendPlaceholder)
	mux.HandleFunc("/", frontendPlaceholder)
	return mux
}

func frontendPlaceholder(writer http.ResponseWriter, request *http.Request) {
	http.NotFound(writer, request)
}
