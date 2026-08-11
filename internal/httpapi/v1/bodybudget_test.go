package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
