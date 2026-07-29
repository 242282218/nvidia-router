package app

import (
	"net/http"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func TestServerReadsShutdownGraceWhenShutdownBegins(t *testing.T) {
	provider := &stubRuntimeSettings{snapshot: runtimeconfig.Snapshot{ShutdownGraceMS: 1000}}
	server := NewServer("127.0.0.1:0", http.NotFoundHandler(), provider)
	provider.snapshot.ShutdownGraceMS = 250

	if got := server.shutdownGrace(); got != 250*time.Millisecond {
		t.Fatalf("shutdown grace = %s, want %s", got, 250*time.Millisecond)
	}
}

type stubRuntimeSettings struct {
	snapshot runtimeconfig.Snapshot
}

func (s *stubRuntimeSettings) Snapshot() runtimeconfig.Snapshot {
	return s.snapshot
}
