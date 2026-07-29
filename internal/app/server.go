package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

type Server struct {
	httpServer *http.Server
	address    string
	settings   runtimeconfig.Provider
}

func NewServer(address string, handler http.Handler, settings runtimeconfig.Provider) *Server {
	return &Server{
		httpServer: &http.Server{Addr: address, Handler: handler},
		address:    address,
		settings:   settings,
	}
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownGrace())
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("wait for HTTP server shutdown: %w", err)
		}
		return nil
	}
}

func (s *Server) shutdownGrace() time.Duration {
	return time.Duration(s.settings.Snapshot().ShutdownGraceMS) * time.Millisecond
}
