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
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- s.httpServer.Serve(listener)
	}()

	select {
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		if s.onShutdown != nil {
			s.onShutdown()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownGrace())
		defer cancel()
		shutdownErr := s.httpServer.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			closeErr := s.httpServer.Close()
			serveErr := <-serveDone
			if errors.Is(serveErr, http.ErrServerClosed) {
				serveErr = nil
			}
			return fmt.Errorf("shutdown HTTP server: %w", errors.Join(shutdownErr, closeErr, serveErr))
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("wait for HTTP server shutdown: %w", err)
		}
		return nil
	}
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
