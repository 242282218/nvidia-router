package v1

import "net/http"

var responseHeaderAllowlist = []string{
	"Content-Type", "Cache-Control", "Retry-After",
	"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
	"Traceparent", "Tracestate", "Baggage",
}

func copyResponseHeaders(dst, src http.Header) {
	for _, name := range responseHeaderAllowlist {
		if values := src.Values(name); len(values) > 0 {
			dst[name] = append([]string(nil), values...)
		}
	}
}
