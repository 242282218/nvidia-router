package apierror

import (
	"encoding/json"
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
