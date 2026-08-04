package apierror

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type response struct {
	Error publicError `json:"error"`
}

type publicError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

func (err Error) Write(writer http.ResponseWriter) {
	if observer, ok := writer.(interface{ SetErrorCode(string) }); ok {
		observer.SetErrorCode(err.Code)
	}
	if err.RetryAfter > 0 {
		writer.Header().Set("Retry-After", strconv.FormatInt(int64((err.RetryAfter+time.Second-1)/time.Second), 10))
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(err.Status)
	_ = json.NewEncoder(writer).Encode(response{
		Error: publicError{
			Message: err.Message,
			Type:    err.Type,
			Param:   err.Param,
			Code:    err.Code,
		},
	})
}

// WriteInternalError emits the generic 500 envelope to the client while
// logging the underlying cause to the provided logger, paired with optional
// request context. The public response carries no detail (per the existing
// envelope contract), but the slog record makes the snack traceable on the
// server side. A nil logger is treated as a no-op so callers without an
// injected logger keep their current behaviour.
func WriteInternalError(writer http.ResponseWriter, logger *slog.Logger, cause error, message string, attrs ...slog.Attr) {
	if logger != nil && cause != nil {
		args := []any{slog.String("cause", cause.Error())}
		args = append(args, toAnySlice(attrs)...)
		logger.Error(message, args...)
	}
	Error{
		Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error",
		Message: "The server could not complete the request.",
	}.Write(writer)
}

func toAnySlice(attrs []slog.Attr) []any {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, attr)
	}
	return out
}
