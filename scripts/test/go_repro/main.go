package main

// Minimal reproduction of the router's static-proxy transport path. Run inside
// the app container:  go-repro <authkey> <session-label>
import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: go-repro <authkey> <session>")
		os.Exit(2)
	}
	proxyURL, err := url.Parse("http://proxy:" + os.Args[1] + "@proxy-pool:8080")
	if err != nil {
		fmt.Println("parse proxy:", err)
		os.Exit(1)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.GetProxyConnectHeader = func(_ context.Context, _ *url.URL, _ string) (http.Header, error) {
		header := make(http.Header)
		header.Set("X-XK-Session", os.Args[2])
		return header, nil
	}
	// The tunnel target (NVIDIA behind a CDN) speaks HTTP/2 regardless of ALPN
	// downgrade; the router's proxy transport disables h2 (ForceAttemptHTTP2=false),
	// so the inner request is HTTP/1.1 and the target's h2 frames break the
	// connection. Toggle via env to confirm.
	if os.Getenv("FORCE_H2") == "1" {
		transport.ForceAttemptHTTP2 = true
	} else {
		transport.ForceAttemptHTTP2 = false
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	request, err := http.NewRequest(http.MethodGet, "https://integrate.api.nvidia.com/v1/models", nil)
	if err != nil {
		fmt.Println("new request:", err)
		os.Exit(1)
	}
	request.Header.Set("Authorization", "Bearer x")
	response, err := client.Do(request)
	if err != nil {
		fmt.Println("ERR:", err)
		os.Exit(1)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 120))
	fmt.Println("OK", response.StatusCode, string(body))
}
