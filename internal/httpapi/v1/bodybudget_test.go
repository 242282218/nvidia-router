package v1

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/apierror"
)

func TestAcquireBodyLeaseChunkedReservesSmallPlaceholder(t *testing.T) {
	// A chunked body has no declared size; reserving the whole endpoint limit
	// meant two concurrent chunked uploads drained the 64MiB pool and every
	// third request (even a small Content-Length one) got 429 server_busy.
	// The reserve is now a small placeholder, so many chunked uploads can be in
	// flight; the actual size is reconciled after the body is read.
	for range maxInFlightBodyBytes/chunkedReserveBytes + 1 {
		lease, err := acquireBodyLease(chunkedBodyRequest(), bodyReadLimitForJSON())
		if err != nil {
			t.Fatalf("acquire chunked lease: %v", err)
		}
		if lease.bytes != chunkedReserveBytes {
			t.Fatalf("chunked reserve = %d, want %d", lease.bytes, chunkedReserveBytes)
		}
		lease.Release()
	}
}

func TestBodyLeaseReconcilesToActualReadSize(t *testing.T) {
	lease, err := acquireBodyLease(chunkedBodyRequest(), bodyReadLimitForJSON())
	if err != nil {
		t.Fatalf("acquire chunked lease: %v", err)
	}
	defer lease.Release()
	before := inFlightBodyBytes.Load()

	// Reading a small body reconciles the placeholder down to the real size.
	lease.reconcile(2048)
	if lease.bytes != 2048 {
		t.Fatalf("reconciled bytes = %d, want 2048", lease.bytes)
	}
	if got := inFlightBodyBytes.Load() - before; got != 2048-chunkedReserveBytes {
		t.Fatalf("in-flight delta = %d, want %d", got, 2048-chunkedReserveBytes)
	}
	lease.releaseSlot()
}

func chunkedBodyRequest() *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
	request.ContentLength = -1
	return request
}

// TestChunkedBodyReadRejectedWhenBudgetExhausted proves the incremental
// pre-reserve enforces the aggregate byte ceiling WHILE a chunked body is
// being read: once the in-flight budget is full, an upload larger than the
// remaining budget fails with 429 instead of reconciling the overrun after
// the bytes are already in memory.
func TestChunkedBodyReadRejectedWhenBudgetExhausted(t *testing.T) {
	// Occupy the byte budget with known-length leases.
	body := strings.Repeat("x", 4<<20)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.ContentLength = int64(len(body))
	var leases []*bodyLease
	for range 32 {
		lease, err := acquireBodyLease(request, bodyReadLimitForJSON())
		if err != nil {
			break
		}
		leases = append(leases, lease)
	}
	if len(leases) == 0 {
		t.Fatal("budget did not fill as expected")
	}
	defer func() {
		for _, lease := range leases {
			lease.Release()
		}
	}()

	// A chunked body larger than the remaining budget must be rejected while
	// reading, and every reservation must be returned.
	chunked := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 2<<20)))
	chunked.ContentLength = -1
	_, _, err := readBodyWithLease(chunked, bodyReadLimitForJSON(), time.Second)
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "server_busy" {
		t.Fatalf("chunked read error = %T %v, want 429 server_busy", err, err)
	}
	if apiErr.RetryAfter <= 0 {
		t.Fatalf("server_busy RetryAfter = %v, want a positive backoff window", apiErr.RetryAfter)
	}
	if got := inFlightBodyBytes.Load(); got != int64(len(leases)*4<<20) {
		t.Fatalf("in-flight bytes after rejection = %d, want only the pre-fill %d", got, len(leases)*4<<20)
	}
}

// TestChunkedBodyReadSucceedsWhenBudgetAllows proves a chunked body is read
// correctly with the incremental pre-reserve and the byte budget is fully
// restored after Release.
func TestChunkedBodyReadSucceedsWhenBudgetAllows(t *testing.T) {
	payload := strings.Repeat("hello", 1<<17)
	chunked := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	chunked.ContentLength = -1
	before := inFlightBodyBytes.Load()

	read, lease, err := readBodyWithLease(chunked, bodyReadLimitForJSON(), time.Second)
	if err != nil {
		t.Fatalf("readBodyWithLease: %v", err)
	}
	if string(read) != payload {
		t.Fatalf("read body length = %d, want %d", len(read), len(payload))
	}
	if got := inFlightBodyBytes.Load(); got != before+int64(len(payload)) {
		t.Fatalf("in-flight during read = %d, want pre-fill %d + payload %d", got, before, len(payload))
	}
	lease.Release()
	if got := inFlightBodyBytes.Load(); got != before {
		t.Fatalf("in-flight after Release = %d, want %d", got, before)
	}
}

func TestChunkedBodyReadRejectsByteAfterLimitAndReleasesLease(t *testing.T) {
	limit := bodyReadLimitForJSON()
	chunked := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.MultiReader(
		strings.NewReader(chunkedLimitFixture()), strings.NewReader("x"),
	))
	chunked.ContentLength = -1
	before := inFlightBodyBytes.Load()

	_, lease, err := readBodyWithLease(chunked, limit, time.Second)
	if lease != nil {
		t.Fatal("oversized chunked body returned a lease")
	}
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "request_too_large" || apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked limit+1 error = %T %v, want 413 request_too_large", err, err)
	}
	if got := inFlightBodyBytes.Load(); got != before {
		t.Fatalf("in-flight bytes after oversized chunked body = %d, want %d", got, before)
	}
}

func TestChunkedBodyReadAcceptsBodyExactlyAtLimit(t *testing.T) {
	limit := bodyReadLimitForJSON()
	payload := chunkedLimitFixture()
	if int64(len(payload)) != limit {
		t.Fatalf("fixture length = %d, want limit %d", len(payload), limit)
	}
	chunked := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	chunked.ContentLength = -1
	before := inFlightBodyBytes.Load()

	read, lease, err := readBodyWithLease(chunked, limit, time.Second)
	if err != nil {
		t.Fatalf("readBodyWithLease: %v", err)
	}
	if lease == nil {
		t.Fatal("exact-limit chunked body returned no lease")
	}
	if string(read) != payload {
		t.Fatalf("read body length = %d, want %d", len(read), len(payload))
	}
	if got := inFlightBodyBytes.Load(); got != before+limit {
		t.Fatalf("in-flight bytes before Release = %d, want %d", got, before+limit)
	}
	lease.Release()
	if got := inFlightBodyBytes.Load(); got != before {
		t.Fatalf("in-flight bytes after Release = %d, want %d", got, before)
	}
}

func chunkedLimitFixture() string {
	return strings.Repeat("x", int(bodyReadLimitForJSON()))
}

// panicReadCloser panics on the first read, simulating a body reader blowing
// up mid-upload.
type panicReadCloser struct{}

func (panicReadCloser) Read([]byte) (int, error) { panic("boom") }
func (panicReadCloser) Close() error             { return nil }

// TestReadBodyWithLeasePanicReleasesBudget proves a panic while reading the
// body cannot leak the semaphore slot or the byte reservation: the recover
// middleware keeps the process alive, so the budget must come back.
func TestReadBodyWithLeasePanicReleasesBudget(t *testing.T) {
	before := inFlightBodyBytes.Load()
	slotsBefore := len(bodyReadSemaphore)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("readBodyWithLease did not propagate the panic")
			}
		}()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", panicReadCloser{})
		request.ContentLength = 100
		_, _, _ = readBodyWithLease(request, bodyReadLimitForJSON(), time.Second)
	}()

	if got := inFlightBodyBytes.Load(); got != before {
		t.Fatalf("in-flight bytes after panic = %d, want %d", got, before)
	}
	if got := len(bodyReadSemaphore); got != slotsBefore {
		t.Fatalf("held read slots after panic = %d, want %d", got, slotsBefore)
	}
}

// TestBodyBudgetReservingReadCloserRejectsOnExhaustedBudget proves the
// streaming wrapper (multipart path) rejects mid-upload once the global byte
// budget cannot cover another read window, instead of reading past the ceiling.
func TestBodyBudgetReservingReadCloserRejectsOnExhaustedBudget(t *testing.T) {
	// Fill the budget with known-length leases.
	body := strings.Repeat("x", 4<<20)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.ContentLength = int64(len(body))
	var leases []*bodyLease
	for range 32 {
		lease, err := acquireBodyLease(request, bodyReadLimitForJSON())
		if err != nil {
			break
		}
		leases = append(leases, lease)
	}
	defer func() {
		for _, lease := range leases {
			lease.Release()
		}
	}()

	chunked := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(strings.Repeat("x", 1<<20)))
	chunked.ContentLength = -1
	wrapped := &budgetReservingReadCloser{ReadCloser: chunked.Body, lease: &bodyLease{bytes: chunkedReserveBytes}}
	_, err := io.ReadAll(io.LimitReader(wrapped, 1<<20))
	if !errors.Is(err, errBodyBudgetExhausted) {
		t.Fatalf("wrapper read error = %v, want errBodyBudgetExhausted", err)
	}
}

func TestBodyBudgetReservingReadCloserChargesShortReadsExactly(t *testing.T) {
	request := chunkedBodyRequest()
	lease, err := acquireBodyLease(request, bodyReadLimitForJSON())
	if err != nil {
		t.Fatalf("acquire chunked lease: %v", err)
	}
	defer lease.Release()

	payload := []byte("short read payload")
	wrapped := &budgetReservingReadCloser{
		ReadCloser: &oneByteReadCloser{payload: payload},
		lease:      lease,
	}
	read, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("read short body: %v", err)
	}
	if string(read) != string(payload) {
		t.Fatalf("read body = %q, want %q", read, payload)
	}
	if got := wrapped.bytesRead(); got != int64(len(payload)) {
		t.Fatalf("charged bytes = %d, want %d", got, len(payload))
	}
}

type oneByteReadCloser struct {
	payload []byte
	offset  int
}

func (r *oneByteReadCloser) Read(payload []byte) (int, error) {
	if r.offset == len(r.payload) {
		return 0, io.EOF
	}
	payload[0] = r.payload[r.offset]
	r.offset++
	return 1, nil
}

func (r *oneByteReadCloser) Close() error { return nil }
