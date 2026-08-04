package v1

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/config"
)

const (
	maxConcurrentBodyReads = 16
	maxInFlightBodyBytes   = 64 << 20
	jsonBodyReadTimeout    = 30 * time.Second
	audioBodyReadTimeout   = 2 * time.Minute
)

// bodyReadSemaphore bounds the number of concurrent body reads. It stays
// separate from the byte budget so small requests cannot starve the process
// with unbounded slow readers.
var bodyReadSemaphore = make(chan struct{}, maxConcurrentBodyReads)
var inFlightBodyBytes atomic.Int64

type bodyLease struct {
	bytes     int64
	slotDone  atomic.Bool
	bytesDone atomic.Bool
}

// releaseSlot returns the read-concurrency slot. It is called as soon as the
// body has been fully read; holding the slot through the rest of the request
// would let a few long-lived streams block every new upload.
func (l *bodyLease) releaseSlot() {
	if l == nil || l.slotDone.Swap(true) {
		return
	}
	<-bodyReadSemaphore
}

func (l *bodyLease) Release() {
	if l == nil || l.bytesDone.Swap(true) {
		return
	}
	inFlightBodyBytes.Add(-l.bytes)
	l.releaseSlot()
}

func acquireBodyLease(request *http.Request, limit int64) (*bodyLease, error) {
	if request == nil || request.Body == nil {
		return nil, invalidBodyRead("The request body is required.")
	}
	if request.ContentLength > limit {
		param := "body"
		return nil, &apierror.Error{
			Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
			Message: "The request body exceeds the endpoint limit.", Param: &param,
		}
	}
	select {
	case bodyReadSemaphore <- struct{}{}:
	default:
		return nil, bodyBusyError()
	}
	reserved := request.ContentLength
	if reserved < 0 {
		// A chunked body can grow to the endpoint limit, so reserve the same
		// budget as a known-size body before reading any bytes.
		reserved = limit
	}
	if reserved == 0 {
		reserved = 1
	}
	for {
		current := inFlightBodyBytes.Load()
		if current+reserved > maxInFlightBodyBytes {
			<-bodyReadSemaphore
			return nil, bodyBusyError()
		}
		if inFlightBodyBytes.CompareAndSwap(current, current+reserved) {
			return &bodyLease{bytes: reserved}, nil
		}
	}
}

func readBodyWithLease(request *http.Request, limit int64, timeout time.Duration) ([]byte, *bodyLease, error) {
	lease, err := acquireBodyLease(request, limit)
	if err != nil {
		return nil, nil, err
	}
	payload, readErr := readBoundedBody(request, limit, timeout)
	// The body is fully in memory once the read returns; release the read slot
	// now so long-lived requests do not monopolize the concurrency budget. The
	// byte budget stays reserved until Release() at the end of the handler.
	lease.releaseSlot()
	if readErr != nil {
		lease.Release()
		return nil, nil, readErr
	}
	return payload, lease, nil
}

func readBoundedBody(request *http.Request, limit int64, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = request.Body.Close() })
	defer stop()
	body, err := io.ReadAll(http.MaxBytesReader(nil, request.Body, limit))
	if err == nil {
		return body, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, &apierror.Error{
			Status: http.StatusRequestTimeout, Type: "invalid_request_error", Code: "request_timeout",
			Message: "The request body took too long to upload.",
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		param := "body"
		return nil, &apierror.Error{
			Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
			Message: "The request body exceeds the endpoint limit.", Param: &param,
		}
	}
	return nil, invalidBodyRead("The request body could not be read.")
}

func bodyBusyError() error {
	return &apierror.Error{
		Status: http.StatusTooManyRequests, Type: "server_error", Code: "server_busy",
		Message: "The server is busy reading request bodies, try again later.",
	}
}

func invalidBodyRead(message string) error {
	return &apierror.Error{Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_request", Message: message}
}

func bodyReadLimitForJSON() int64 { return config.JSONBodyLimit }
