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
	// chunkedReserveBytes is the placeholder byte budget reserved for a chunked
	// body before its size is known. Reserving the whole endpoint limit meant
	// two concurrent chunked uploads exhausted the 64MiB pool and every third
	// request (even a small Content-Length one) got 429 server_busy. The reserve
	// only marks "a chunked body is in flight"; the actual size is charged
	// incrementally as the body is read (see readChunkedBody), and MaxBytesReader
	// still caps each request.
	chunkedReserveBytes = 1 << 20
	// bodyBudgetChunkSize is the read window used to pre-reserve the global byte
	// budget for chunked bodies. Reserving per chunk enforces the aggregate
	// ceiling while bytes are being read instead of reconciling after the fact.
	bodyBudgetChunkSize = 256 << 10
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

// tryReserve charges more bytes to the global in-flight budget, rejecting the
// charge when it would exceed the ceiling. Chunked bodies call it incrementally
// while reading so the aggregate cap is enforced before the bytes land in
// memory, not reconciled afterwards.
func (l *bodyLease) tryReserve(bytes int64) bool {
	if l == nil || bytes <= 0 {
		return true
	}
	for {
		current := inFlightBodyBytes.Load()
		if current+bytes > maxInFlightBodyBytes {
			return false
		}
		if inFlightBodyBytes.CompareAndSwap(current, current+bytes) {
			l.bytes += bytes
			return true
		}
	}
}

// reconcile adjusts the reserved byte budget to the actual body size once the
// body has been read. A chunked body reserved a small placeholder; charge the
// difference so concurrent in-flight bytes track reality instead of either the
// oversized limit reserve or the undersized placeholder. After the incremental
// pre-reserve in readChunkedBody the delta is at most one read window, so the
// adjustment cannot meaningfully overshoot the budget.
func (l *bodyLease) reconcile(actual int64) {
	if l == nil {
		return
	}
	delta := actual - l.bytes
	if delta == 0 {
		return
	}
	inFlightBodyBytes.Add(delta)
	l.bytes = actual
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
		// A chunked body has no declared size; reserve a small placeholder and
		// reconcile to the actual read size once the body is consumed. Reserving
		// the endpoint limit let two chunked uploads drain the whole pool.
		reserved = chunkedReserveBytes
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

// readBodyWithLease reads the request body within the endpoint limit while
// holding a read-slot and byte-budget lease. Known-length bodies reserve their
// exact size up front; chunked bodies reserve incrementally as bytes arrive so
// concurrent chunked uploads cannot collectively blow through the global byte
// budget after the fact.
func readBodyWithLease(request *http.Request, limit int64, timeout time.Duration) (payload []byte, lease *bodyLease, err error) {
	lease, err = acquireBodyLease(request, limit)
	if err != nil {
		return nil, nil, err
	}
	// A panic between acquiring the lease and returning it must not leak the
	// semaphore slot or the byte reservation: the recover middleware keeps the
	// process alive, but the budget would stay consumed forever.
	owned := false
	defer func() {
		if !owned {
			lease.Release()
		}
	}()
	if request.ContentLength < 0 {
		payload, err = readChunkedBody(request, limit, timeout, lease)
	} else {
		payload, err = readBoundedBody(request, limit, timeout)
	}
	// The body is fully in memory once the read returns; release the read slot
	// now so long-lived requests do not monopolize the concurrency budget. The
	// byte budget stays reserved until Release() at the end of the handler,
	// reconciled to the actual size read.
	lease.reconcile(int64(len(payload)))
	lease.releaseSlot()
	if err != nil {
		lease.Release()
		return nil, nil, err
	}
	owned = true
	return payload, lease, nil
}

// readChunkedBody reads a body whose size is not declared up front, reserving
// the global byte budget incrementally as each window is pulled into memory. A
// window that cannot be reserved means the aggregate in-flight bytes are at the
// ceiling: abort with 429 before more data lands in memory. The old path read
// the whole chunked body and reconciled afterwards, so sixteen concurrent
// chunked uploads could hold up to 16× the endpoint limit in memory against a
// 64MiB budget.
func readChunkedBody(request *http.Request, limit int64, timeout time.Duration, lease *bodyLease) ([]byte, error) {
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = request.Body.Close() })
	defer stop()

	var out []byte
	buf := make([]byte, bodyBudgetChunkSize)
	limited := http.MaxBytesReader(nil, request.Body, limit+1)
	for {
		remaining := limit + 1 - int64(len(out))
		if remaining <= 0 {
			break
		}
		readLen := int(remaining)
		if readLen > len(buf) {
			readLen = len(buf)
		}
		if !lease.tryReserve(int64(readLen)) {
			return nil, bodyBusyError()
		}
		n, readErr := limited.Read(buf[:readLen])
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if int64(len(out)) > limit {
			return nil, classifyBodyReadError(ctx, &http.MaxBytesError{Limit: limit})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, classifyBodyReadError(ctx, readErr)
		}
		if n == 0 {
			break
		}
	}
	return out, nil
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
	return nil, classifyBodyReadError(ctx, err)
}

// classifyBodyReadError maps the shared read failure modes (upload deadline,
// per-request size cap, connection) to their structured errors.
func classifyBodyReadError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &apierror.Error{
			Status: http.StatusRequestTimeout, Type: "invalid_request_error", Code: "request_timeout",
			Message: "The request body took too long to upload.",
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		param := "body"
		return &apierror.Error{
			Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
			Message: "The request body exceeds the endpoint limit.", Param: &param,
		}
	}
	return invalidBodyRead("The request body could not be read.")
}

// errBodyBudgetExhausted is returned by budgetReservingReadCloser when the
// global in-flight byte budget is exhausted before the body has been fully
// read. Callers that stream the body (multipart parsing) map it to 429
// server_busy.
var errBodyBudgetExhausted = errors.New("in-flight body byte budget exhausted")

// budgetReservingReadCloser wraps a body that is consumed outside
// readBodyWithLease (multipart parsing) and charges the global byte budget
// incrementally as bytes are pulled out, capping each read at the remaining
// budget. A chunked multipart upload therefore cannot overrun the aggregate
// ceiling after the fact: the reader is rejected mid-upload instead.
type budgetReservingReadCloser struct {
	io.ReadCloser
	lease *bodyLease
}

// bytesRead reports how many bytes this wrapper charged to the budget (past the
// chunked placeholder), so the caller can reconcile the reservation.
func (r *budgetReservingReadCloser) bytesRead() int64 { return r.lease.bytes - chunkedReserveBytes }

func (r *budgetReservingReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Charge the global budget for the window about to be read. tryReserve is
	// atomic; on a lost race the window shrinks instead of reading past the
	// ceiling, and at one byte the charge either succeeds or the budget is
	// genuinely exhausted.
	maxLen := len(p)
	for {
		if r.lease.tryReserve(int64(maxLen)) {
			break
		}
		maxLen /= 2
		if maxLen == 0 {
			return 0, errBodyBudgetExhausted
		}
	}
	n, err := r.ReadCloser.Read(p[:maxLen])
	return n, err
}

func bodyBusyError() error {
	return &apierror.Error{
		Status: http.StatusTooManyRequests, Type: "server_error", Code: "server_busy",
		Message: "The server is busy reading request bodies, try again later.",
		// Body reads are short (bounded by jsonBodyReadTimeout), so a 1s retry
		// window is enough for the in-flight read to release its slot. Keeping the
		// header consistent with the other 429 surfaces (queue_full/queue_timeout)
		// lets clients back off uniformly instead of hammering immediately.
		RetryAfter: time.Second,
	}
}

func invalidBodyRead(message string) error {
	return &apierror.Error{Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_request", Message: message}
}

func bodyReadLimitForJSON() int64 { return config.JSONBodyLimit }
