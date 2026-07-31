package xkproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/runtimeconfig"
)

func TestManagerDoesNotFetchBeforeFirstAcquire(t *testing.T) {
	var calls atomic.Int32
	manager := newTestManager(t, func(context.Context) (*url.URL, error) {
		calls.Add(1)
		return testProxyURL("192.0.2.10:8000"), nil
	})
	manager.Close()
	if calls.Load() != 0 {
		t.Fatalf("fetch calls = %d, want 0", calls.Load())
	}
}

func TestManagerCoalescesConcurrentInitialAcquire(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	manager := newTestManager(t, func(context.Context) (*url.URL, error) {
		calls.Add(1)
		close(started)
		<-release
		return testProxyURL("192.0.2.10:8000"), nil
	})

	const count = 50
	handles := make(chan *Handle, count)
	errorsCh := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000})
			if err != nil {
				errorsCh <- err
				return
			}
			handles <- handle
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	close(release)
	group.Wait()
	close(handles)
	close(errorsCh)
	if calls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls.Load())
	}
	var leaseID int64
	for handle := range handles {
		if leaseID == 0 {
			leaseID = handle.lease.id
		} else if handle.lease.id != leaseID {
			t.Fatalf("lease ID = %d, want %d", handle.lease.id, leaseID)
		}
		handle.Release()
	}
	for err := range errorsCh {
		t.Fatalf("Acquire: %v", err)
	}
	manager.Close()
}

func TestManagerRefreshesAtUsableBoundary(t *testing.T) {
	start := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	source := &testClock{now: start}
	var calls atomic.Int32
	manager := newTestManagerWithClock(t, source, func(context.Context) (*url.URL, error) {
		if calls.Add(1) == 1 {
			return testProxyURL("192.0.2.10:8000"), nil
		}
		return testProxyURL("192.0.2.11:8000"), nil
	})

	first, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstID := first.lease.id
	first.Release()
	source.now = start.Add(165 * time.Second)
	second, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer second.Release()
	if second.lease.id == firstID {
		t.Fatalf("lease ID = %d, want replacement for %d", second.lease.id, firstID)
	}
	if calls.Load() != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls.Load())
	}
}

func TestManagerDoesNotCreateLeaseAfterFetchContextCancellation(t *testing.T) {
	started := make(chan struct{})
	manager := newTestManager(t, func(ctx context.Context) (*url.URL, error) {
		close(started)
		<-ctx.Done()
		return testProxyURL("192.0.2.10:8000"), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		handle *Handle
		err    error
	}, 1)
	go func() {
		handle, err := manager.Acquire(ctx, runtimeconfig.Snapshot{})
		done <- struct {
			handle *Handle
			err    error
		}{handle: handle, err: err}
	}()
	<-started
	cancel()
	result := <-done
	if result.handle != nil {
		result.handle.Release()
		t.Fatal("Acquire returned a handle after context cancellation")
	}
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want context canceled", result.err)
	}

	manager.mu.Lock()
	active := manager.active
	nextID := manager.nextID
	fetchCount := manager.fetchCount
	manager.mu.Unlock()
	if active != nil {
		t.Fatal("canceled Acquire left an active lease")
	}
	if nextID != 0 || fetchCount != 0 {
		t.Fatalf("canceled Acquire left metrics: nextID=%d fetchCount=%d", nextID, fetchCount)
	}
	manager.Close()
}

func TestManagerDoesNotCreateLeaseWhenCallerCancelsBeforeFetcherReturnsSuccess(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	manager := newTestManager(t, func(context.Context) (*url.URL, error) {
		close(started)
		<-release
		return testProxyURL("192.0.2.10:8000"), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		handle *Handle
		err    error
	}, 1)
	go func() {
		handle, err := manager.Acquire(ctx, runtimeconfig.Snapshot{})
		done <- struct {
			handle *Handle
			err    error
		}{handle: handle, err: err}
	}()
	<-started
	cancel()
	close(release)

	result := <-done
	if result.handle != nil {
		result.handle.Release()
		t.Fatal("Acquire returned a handle after caller context cancellation")
	}
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want context canceled", result.err)
	}
	manager.Close()
}

func TestManagerRetireKeepsActiveReferenceUntilRelease(t *testing.T) {
	var calls atomic.Int32
	manager := newTestManager(t, func(context.Context) (*url.URL, error) {
		if calls.Add(1) == 1 {
			return testProxyURL("192.0.2.10:8000"), nil
		}
		return testProxyURL("192.0.2.11:8000"), nil
	})
	first, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	transport := first.Transport()
	first.Retire(RetireReasonTransportError)
	first.Retire(RetireReasonTransportError)
	second, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if second.lease.id == first.lease.id {
		t.Fatalf("second lease reused retired lease %d", first.lease.id)
	}
	first.Release()
	first.Release()
	manager.mu.Lock()
	_, retained := manager.retiring[first.lease]
	manager.mu.Unlock()
	if retained {
		t.Fatal("retired lease retained after final release")
	}
	if transport == second.Transport() {
		t.Fatal("retired transport was reused")
	}
	second.Release()
	manager.Close()
}

func TestManagerRetireAfterReleaseStillRetiresLease(t *testing.T) {
	var calls atomic.Int32
	manager := newTestManager(t, func(context.Context) (*url.URL, error) {
		if calls.Add(1) == 1 {
			return testProxyURL("192.0.2.10:8000"), nil
		}
		return testProxyURL("192.0.2.11:8000"), nil
	})

	first, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	firstID := first.lease.id
	first.Release()
	first.Retire(RetireReasonTransportError)
	first.Retire(RetireReasonTransportError)

	second, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer second.Release()
	if second.lease.id == firstID {
		t.Fatalf("second lease reused lease %d retired after release", firstID)
	}
	if calls.Load() != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls.Load())
	}
	manager.Close()
}

func TestManagerNegativeCachesFetchFailure(t *testing.T) {
	start := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	source := &testClock{now: start}
	var calls atomic.Int32
	manager := newTestManagerWithClock(t, source, func(context.Context) (*url.URL, error) {
		calls.Add(1)
		return nil, newError(ReasonInvalidResponse, errors.New("private response"))
	})

	for range 2 {
		_, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
		var proxyErr *Error
		if !errors.As(err, &proxyErr) {
			t.Fatalf("Acquire error = %T %v, want *Error", err, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls.Load())
	}
	source.now = start.Add(time.Second)
	_, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err == nil || calls.Load() != 2 {
		t.Fatalf("Acquire after negative cache: err=%v calls=%d", err, calls.Load())
	}
	manager.Close()
}

func TestManagerWaitCancellationAndClose(t *testing.T) {
	started := make(chan struct{})
	manager := newTestManager(t, func(ctx context.Context) (*url.URL, error) {
		close(started)
		<-ctx.Done()
		return nil, newError(ReasonFetchFailed, ctx.Err())
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
		firstDone <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(ctx, runtimeconfig.Snapshot{})
		secondDone <- err
	}()
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting Acquire error = %v, want context canceled", err)
	}
	manager.Close()
	if err := <-firstDone; err == nil {
		t.Fatalf("fetching Acquire error = %v, want proxy error", err)
	} else {
		var proxyErr *Error
		if !errors.As(err, &proxyErr) {
			t.Fatalf("fetching Acquire error = %T %v, want proxy error", err, err)
		}
	}
}

func TestManagerCachesTransportByStableTimeoutKey(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	manager := newTestManagerWithBase(t, base, func(context.Context) (*url.URL, error) {
		return testProxyURL("192.0.2.10:8000"), nil
	})
	first, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 125, FirstByteTimeoutMS: 250})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 125, FirstByteTimeoutMS: 250})
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	third, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 500, FirstByteTimeoutMS: 750})
	if err != nil {
		t.Fatalf("third Acquire: %v", err)
	}
	firstTransport := first.Transport().(*http.Transport)
	if firstTransport != second.Transport() {
		t.Fatal("same timeout key did not reuse Transport")
	}
	if firstTransport == third.Transport() {
		t.Fatal("different timeout key reused Transport")
	}
	if firstTransport.MaxIdleConns != 64 || firstTransport.MaxIdleConnsPerHost != 32 || firstTransport.IdleConnTimeout != 60*time.Second || !firstTransport.ForceAttemptHTTP2 {
		t.Fatalf("transport settings = %#v", firstTransport)
	}
	if firstTransport.TLSClientConfig == nil || firstTransport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("base TLS settings were not preserved")
	}
	first.Release()
	second.Release()
	third.Release()
	manager.Close()
}

func TestManagerLogsDoNotContainProxySecrets(t *testing.T) {
	var logs bytes.Buffer
	manager := newManagerWithFetcher(
		testProxyURL("198.51.100.1:8000"), 3*time.Minute, 15*time.Second,
		http.DefaultTransport.(*http.Transport), &testClock{now: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)},
		slog.New(slog.NewTextHandler(&logs, nil)),
		func(context.Context) (*url.URL, error) {
			return testProxyURL("203.0.113.10:65535"), nil
		},
	)
	handle, err := manager.Acquire(context.Background(), runtimeconfig.Snapshot{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	handle.Retire(RetireReasonTransportError)
	handle.Release()
	manager.Close()

	for _, secret := range []string{"203.0.113.10", "65535", "apikey", "sign", "secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log contains %q: %s", secret, logs.String())
		}
	}
}

func newTestManager(t *testing.T, fetch func(context.Context) (*url.URL, error)) *Manager {
	t.Helper()
	return newTestManagerWithClock(t, &testClock{now: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)}, fetch)
}

func newTestManagerWithClock(t *testing.T, source *testClock, fetch func(context.Context) (*url.URL, error)) *Manager {
	t.Helper()
	return newManagerWithFetcher(
		testProxyURL("198.51.100.1:8000"), 3*time.Minute, 15*time.Second,
		http.DefaultTransport.(*http.Transport), source, slog.New(slog.NewTextHandler(io.Discard, nil)), fetch,
	)
}

func newTestManagerWithBase(t *testing.T, base *http.Transport, fetch func(context.Context) (*url.URL, error)) *Manager {
	t.Helper()
	manager := newManagerWithFetcher(
		testProxyURL("198.51.100.1:8000"), 3*time.Minute, 15*time.Second,
		base, &testClock{now: time.Now()}, slog.New(slog.NewTextHandler(io.Discard, nil)), fetch,
	)
	return manager
}

func testProxyURL(host string) *url.URL {
	return &url.URL{Scheme: "http", Host: host}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) NewTimer(duration time.Duration) *time.Timer { return time.NewTimer(duration) }

func (c *testClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}

var _ clock.Clock = (*testClock)(nil)
