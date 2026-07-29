package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func TestServerReadsShutdownGraceWhenShutdownBegins(t *testing.T) {
	provider := &stubRuntimeSettings{snapshot: runtimeconfig.Snapshot{ShutdownGraceMS: 1000}}
	server := NewServer("127.0.0.1:0", http.NotFoundHandler(), provider, nil)
	provider.snapshot.ShutdownGraceMS = 250

	if got := server.shutdownGrace(); got != 250*time.Millisecond {
		t.Fatalf("shutdown grace = %s, want %s", got, 250*time.Millisecond)
	}
}

func TestServerForceClosesAfterShutdownTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listener: %v", err)
	}
	address := listener.Addr().String()
	listener.Close()
	handlerStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	server := NewServer(address, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(handlerStarted)
		<-request.Context().Done()
		close(handlerDone)
	}), &stubRuntimeSettings{snapshot: runtimeconfig.Snapshot{ShutdownGraceMS: 1}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ListenAndServe(ctx) }()
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			response, err := http.Get("http://" + address)
			if err == nil {
				response.Body.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-serveDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListenAndServe error = %v, want shutdown timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish after forced close")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("blocking handler was not released")
	}
}

type stubRuntimeSettings struct {
	snapshot runtimeconfig.Snapshot
}

func (s *stubRuntimeSettings) Snapshot() runtimeconfig.Snapshot {
	return s.snapshot
}
