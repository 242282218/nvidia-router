package httpapi

import (
	"net/http"
	"strings"

	"nvidia-router/internal/accesskey"
)

// NoStoreMiddleware marks API responses as non-cacheable without replacing
// existing Cache-Control directives.
func NoStoreMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		next.ServeHTTP(&noStoreResponseWriter{ResponseWriter: writer}, request)
	})
}

type noStoreResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (writer *noStoreResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *noStoreResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	ensureNoStore(writer.Header())
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *noStoreResponseWriter) Write(body []byte) (int, error) {
	writer.WriteHeader(http.StatusOK)
	return writer.ResponseWriter.Write(body)
}

func (writer *noStoreResponseWriter) Flush() {
	writer.WriteHeader(http.StatusOK)
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func ensureNoStore(header http.Header) {
	cacheControl := header.Get("Cache-Control")
	if cacheControl == "" {
		header.Set("Cache-Control", "no-store")
	} else if !hasCacheDirective(cacheControl, "no-store") {
		header.Set("Cache-Control", cacheControl+", no-store")
	}
}

func hasCacheDirective(value, directive string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(item, "=", 2)[0]), directive) {
			return true
		}
	}
	return false
}

func DataMiddleware(keys *accesskey.Service, next http.Handler) http.Handler {
	return accesskey.Middleware(keys, next)
}
