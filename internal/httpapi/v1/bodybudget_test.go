package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAcquireBodyLeaseReservesFullLimitForChunkedBody(t *testing.T) {
	first, err := acquireBodyLease(chunkedBodyRequest(), bodyReadLimitForJSON())
	if err != nil {
		t.Fatalf("acquire first chunked lease: %v", err)
	}
	defer first.Release()

	second, err := acquireBodyLease(chunkedBodyRequest(), bodyReadLimitForJSON())
	if err != nil {
		t.Fatalf("acquire second chunked lease: %v", err)
	}
	defer second.Release()

	if _, err := acquireBodyLease(chunkedBodyRequest(), bodyReadLimitForJSON()); err == nil {
		t.Fatal("third chunked lease succeeded, want byte-budget rejection")
	}
}

func chunkedBodyRequest() *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
	request.ContentLength = -1
	return request
}
