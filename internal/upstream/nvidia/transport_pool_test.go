package nvidia

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

// TestDirectTransportSizesConnectionPool locks in the fix for direct mode
// inheriting http.DefaultTransport's MaxIdleConnsPerHost of 2. Every NVIDIA key
// targets the same host, so that default capped reusable connections at two and
// forced a fresh TCP+TLS handshake per concurrent request beyond it — while the
// proxy path already configured 32. The two paths must now agree.
func TestDirectTransportSizesConnectionPool(t *testing.T) {
	transport, _ := newAttemptTransport(http.DefaultTransport, runtimeconfig.Snapshot{
		ConnectTimeoutMS: 5000, FirstByteTimeoutMS: 30000,
	})
	clone, ok := transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", transport)
	}
	if clone.MaxIdleConnsPerHost != 32 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 32 (matching the proxy path)", clone.MaxIdleConnsPerHost)
	}
	if clone.MaxIdleConns != 64 {
		t.Fatalf("MaxIdleConns = %d, want 64", clone.MaxIdleConns)
	}
	if clone.IdleConnTimeout != 60*time.Second {
		t.Fatalf("IdleConnTimeout = %s, want 60s", clone.IdleConnTimeout)
	}
}

// TestDirectTransportStillAppliesTimeouts guards that adding pool sizing did not
// displace the per-attempt timeout wiring the budget depends on.
func TestDirectTransportStillAppliesTimeouts(t *testing.T) {
	transport, dialer := newAttemptTransport(http.DefaultTransport, runtimeconfig.Snapshot{
		ConnectTimeoutMS: 2500, FirstByteTimeoutMS: 7000,
	})
	clone, ok := transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", transport)
	}
	if clone.ResponseHeaderTimeout != 7*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %s, want 7s", clone.ResponseHeaderTimeout)
	}
	if dialer.Timeout != 2500*time.Millisecond {
		t.Fatalf("dialer timeout = %s, want 2.5s", dialer.Timeout)
	}
}

// TestDirectTransportPoolReusesByTimeoutKey confirms pool sizing did not break
// transport reuse: identical timeouts must return the same transport so
// keep-alive connections survive across requests.
func TestDirectTransportPoolReusesByTimeoutKey(t *testing.T) {
	pool := newDirectTransportPool(http.DefaultTransport)
	defer pool.Close()

	settings := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	first := pool.Get(settings)
	second := pool.Get(settings)
	if first != second {
		t.Fatal("identical timeout settings returned different transports; keep-alive pools would be discarded")
	}
	other := pool.Get(runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 9999})
	if other == first {
		t.Fatal("different first-byte timeout reused the same transport")
	}
}

func TestDirectTransportPoolConcurrentGet(t *testing.T) {
	pool := newDirectTransportPool(http.DefaultTransport)
	defer pool.Close()

	const (
		workers    = 32
		iterations = 200
	)
	settings := runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
	pool.Get(settings)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				pool.Get(settings)
			}
		}()
	}
	close(start)
	group.Wait()

	wantClock := uint64(1 + workers*iterations)
	if got := pool.clock.Load(); got != wantClock {
		t.Fatalf("access clock = %d, want %d after %d concurrent hits", got, wantClock, workers*iterations)
	}
}
