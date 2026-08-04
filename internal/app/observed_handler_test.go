package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/observability"
)

func TestObservedHandlerRecordsPanicAs500(t *testing.T) {
	recorder := &panicRequestRecorder{}
	handler := observedHandler(recorder, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if recorder.record.HTTPStatus != http.StatusInternalServerError || recorder.record.Outcome != observability.OutcomeFailure {
		t.Fatalf("panic record = %#v, want failed 500", recorder.record)
	}
}

type panicRequestRecorder struct {
	record observability.RequestRecord
}

func (r *panicRequestRecorder) Record(_ context.Context, record observability.RequestRecord) error {
	r.record = record
	return nil
}
