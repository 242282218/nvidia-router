package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"nvidia-router/internal/apierror"
)

// RecoverMiddleware turns handler panics into a structured 500 response and a
// logged stack. net/http otherwise closes the connection on panic, leaving the
// client with a truncated body and no diagnosable error record.
func RecoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder := &recoverResponseWriter{ResponseWriter: writer}
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// Preserve the standard library's abort-handler contract: a handler
			// that intentionally panics with ErrAbortHandler wants the raw
			// connection teardown, not a 500 body.
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			logger.Error("panic serving request",
				"method", request.Method,
				"path", request.URL.Path,
				"panic", fmt.Sprintf("%v", recovered),
				"stack", string(debug.Stack()),
			)
			// Once headers are committed (SSE streams) nothing coherent can be
			// written; the broken stream is the client-visible signal.
			if recorder.wroteHeader {
				return
			}
			apierror.Error{
				Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
				Message: "An internal error occurred.",
			}.Write(recorder)
		}()
		next.ServeHTTP(recorder, request)
	})
}

type recoverResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (writer *recoverResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *recoverResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *recoverResponseWriter) Write(body []byte) (int, error) {
	writer.WriteHeader(http.StatusOK)
	return writer.ResponseWriter.Write(body)
}

func (writer *recoverResponseWriter) Flush() {
	writer.WriteHeader(http.StatusOK)
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
