package apierror

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestErrorWriteReturnsOnlyPublicOpenAIErrorFields(t *testing.T) {
	param := "model"
	apiErr := Error{
		Status:  http.StatusBadRequest,
		Type:    "invalid_request_error",
		Code:    "invalid_model",
		Message: "The requested model is unavailable.",
		Param:   &param,
		Cause: errors.New(
			"Authorization: Bearer nvapi-secret https://internal.example.invalid/debug stack: upstreamCall",
		),
	}
	recorder := httptest.NewRecorder()

	apiErr.Write(recorder)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", contentType, "application/json")
	}
	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter != "" {
		t.Fatalf("Retry-After = %q, want empty", retryAfter)
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 1 {
		t.Fatalf("response has %d fields, want 1: %s", len(response), recorder.Body.String())
	}

	payload, ok := response["error"]
	if !ok {
		t.Fatalf("response missing error field: %s", recorder.Body.String())
	}
	var publicFields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &publicFields); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if len(publicFields) != 4 {
		t.Fatalf("error payload has %d fields, want 4: %s", len(publicFields), payload)
	}
	for _, name := range []string{"message", "type", "param", "code"} {
		if _, ok := publicFields[name]; !ok {
			t.Fatalf("error payload missing %q: %s", name, payload)
		}
	}

	var got struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Param   *string `json:"param"`
		Code    string  `json:"code"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode public error: %v", err)
	}
	if got.Message != apiErr.Message || got.Type != apiErr.Type || got.Param == nil || *got.Param != param || got.Code != apiErr.Code {
		t.Fatalf("public error = %+v, want message/type/param/code from API error", got)
	}
	for _, sensitive := range []string{"nvapi-secret", "internal.example.invalid", "upstreamCall"} {
		if strings.Contains(recorder.Body.String(), sensitive) {
			t.Fatalf("response leaked %q: %s", sensitive, recorder.Body.String())
		}
	}
}

func TestErrorWriteRoundsRetryAfterUpToWholeSeconds(t *testing.T) {
	recorder := httptest.NewRecorder()

	Error{
		Status:     http.StatusTooManyRequests,
		Type:       "rate_limit_error",
		Code:       "rate_limit_exceeded",
		Message:    "Please retry later.",
		RetryAfter: 1501 * time.Millisecond,
	}.Write(recorder)

	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter != "2" {
		t.Fatalf("Retry-After = %q, want %q", retryAfter, "2")
	}
}

func TestErrorWriteOmitsRetryAfterForNonRetryError(t *testing.T) {
	recorder := httptest.NewRecorder()

	Error{
		Status:  http.StatusBadRequest,
		Type:    "invalid_request_error",
		Code:    "invalid_model",
		Message: "The requested model is unavailable.",
	}.Write(recorder)

	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter != "" {
		t.Fatalf("Retry-After = %q, want empty", retryAfter)
	}
}

func TestErrorStringExposesOnlyPublicMessage(t *testing.T) {
	err := Error{Message: "safe public message", Cause: errors.New("Bearer nvapi-secret")}

	if got := err.Error(); got != "safe public message" {
		t.Fatalf("Error() = %q, want public message", got)
	}
}
