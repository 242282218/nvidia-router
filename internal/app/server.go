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

	lifecycleMu      sync.Mutex
	listener         net.Listener
	serveFinished    chan struct{}
	serveStarted     bool
	serveErr         error
	shutdownDone     chan struct{}
	shutdownStarted  bool
	shutdownErr      error
	shutdownDeadline time.Time
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

func (s *Server) setShutdownDeadline(deadline time.Time) {
	if deadline.IsZero() {
		return
	}
	s.lifecycleMu.Lock()
	if s.shutdownDeadline.IsZero() {
		s.shutdownDeadline = deadline
	}
	s.lifecycleMu.Unlock()
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.lifecycleMu.Lock()
	if s.shutdownStarted {
		s.lifecycleMu.Unlock()
		return nil
	}
	if s.serveStarted {
		s.lifecycleMu.Unlock()
		return errors.New("serve HTTP: server already serving")
	}
	serveFinished := make(chan struct{})
	s.serveFinished = serveFinished
	s.serveStarted = true
	s.lifecycleMu.Unlock()

	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		s.finishServe(fmt.Errorf("listen on %s: %w", s.address, err))
		return s.serveError()
	}

	s.lifecycleMu.Lock()
	if s.shutdownStarted {
		s.lifecycleMu.Unlock()
		_ = listener.Close()
		s.finishServe(nil)
		return nil
	}
	s.listener = listener
	s.lifecycleMu.Unlock()

	go func() {
		err := s.httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.finishServe(err)
	}()

	select {
	case <-serveFinished:
		if err := s.serveError(); err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		return s.shutdown(context.Background(), false)
	}
}

// Shutdown stops accepting new connections, drains active requests for the
// configured grace period, and force-closes connections when the grace ends.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.shutdown(ctx, true)
}

func (s *Server) shutdown(ctx context.Context, waitServe bool) error {
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
	serveFinished := s.serveFinished
	hasListener := s.listener != nil
	s.lifecycleMu.Unlock()

	if s.onShutdown != nil {
		s.onShutdown()
	}

	s.lifecycleMu.Lock()
	deadline := s.shutdownDeadline
	if deadline.IsZero() {
		deadline = time.Now().Add(s.shutdownGrace())
		s.shutdownDeadline = deadline
	}
	s.lifecycleMu.Unlock()
	if ctxDeadline, ok := ctx.Deadline(); !ok || deadline.Before(ctxDeadline) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}

	var shutdownErr error
	if hasListener {
		shutdownErr = s.httpServer.Shutdown(ctx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, s.httpServer.Close())
		}
	}
	finish := func(shutdownErr error) error {
		if serveFinished != nil {
			<-serveFinished
			if hasListener {
				if serveErr := s.serveError(); serveErr != nil {
					shutdownErr = errors.Join(shutdownErr, serveErr)
				}
			}
		}
		if shutdownErr != nil {
			shutdownErr = fmt.Errorf("shutdown HTTP server: %w", shutdownErr)
		}
		s.lifecycleMu.Lock()
		s.shutdownErr = shutdownErr
		close(s.shutdownDone)
		s.lifecycleMu.Unlock()
		return shutdownErr
	}
	if waitServe {
		return finish(shutdownErr)
	}
	go func(err error) { _ = finish(err) }(shutdownErr)
	return shutdownErr
}

func (s *Server) finishServe(err error) {
	s.lifecycleMu.Lock()
	s.serveErr = err
	s.listener = nil
	finished := s.serveFinished
	if finished != nil {
		close(finished)
	}
	s.lifecycleMu.Unlock()
}

func (s *Server) serveError() error {
	s.lifecycleMu.Lock()
	err := s.serveErr
	s.lifecycleMu.Unlock()
	return err
}

func (s *Server) waitServeDone() {
	s.lifecycleMu.Lock()
	finished := s.serveFinished
	s.lifecycleMu.Unlock()
	if finished != nil {
		<-finished
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
