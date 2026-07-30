package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

type Server struct {
	httpServer *http.Server
	address    string
	settings   runtimeconfig.Provider
	onShutdown func()
	graceMu    sync.RWMutex
	grace      time.Duration

	lifecycleMu     sync.Mutex
	listener        net.Listener
	serveDone       chan error
	shutdownDone    chan struct{}
	shutdownStarted bool
	shutdownErr     error
}

func NewServer(address string, handler http.Handler, settings runtimeconfig.Provider, onShutdown func()) *Server {
	return &Server{
		httpServer: &http.Server{Addr: address, Handler: handler},
		address:    address,
		settings:   settings,
		onShutdown: onShutdown,
	}
}

func (s *Server) setRootContext(ctx context.Context) {
	if ctx != nil {
		s.httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
	}
}

func (s *Server) setShutdownGrace(grace time.Duration) {
	s.graceMu.Lock()
	s.grace = grace
	s.graceMu.Unlock()
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.address, err)
	}

	s.lifecycleMu.Lock()
	if s.shutdownStarted {
		s.lifecycleMu.Unlock()
		_ = listener.Close()
		return nil
	}
	serveDone := make(chan error, 1)
	s.listener = listener
	s.serveDone = serveDone
	s.lifecycleMu.Unlock()

	go func() { serveDone <- s.httpServer.Serve(listener) }()
	select {
	case err := <-serveDone:
		s.recordServeDone(serveDone)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	}
}

// Shutdown stops accepting new connections, drains active requests for the
// configured grace period, and force-closes connections when the grace ends.
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.lifecycleMu.Lock()
	if s.shutdownStarted {
		done := s.shutdownDone
		s.lifecycleMu.Unlock()
		<-done
		return s.shutdownErr
	}
	s.shutdownStarted = true
	s.shutdownDone = make(chan struct{})
	listener := s.listener
	s.lifecycleMu.Unlock()

	if s.onShutdown != nil {
		s.onShutdown()
	}
	shutdownCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		shutdownCtx, cancel = context.WithTimeout(ctx, s.shutdownGrace())
	}
	defer cancel()

	var shutdownErr error
	if listener != nil {
		shutdownErr = s.httpServer.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, s.httpServer.Close())
		}
	}

	s.lifecycleMu.Lock()
	s.shutdownErr = shutdownErr
	close(s.shutdownDone)
	s.lifecycleMu.Unlock()
	return shutdownErr
}

func (s *Server) recordServeDone(serveDone chan error) {
	s.lifecycleMu.Lock()
	if s.serveDone == serveDone {
		s.listener = nil
	}
	s.lifecycleMu.Unlock()
}

func (s *Server) shutdownGrace() time.Duration {
	s.graceMu.RLock()
	grace := s.grace
	s.graceMu.RUnlock()
	if grace > 0 {
		return grace
	}
	if s.settings == nil {
		return defaultShutdownGrace
	}
	grace = time.Duration(s.settings.Snapshot().ShutdownGraceMS) * time.Millisecond
	if grace <= 0 {
		return defaultShutdownGrace
	}
	return grace
}
