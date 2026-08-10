package xkproxy

import (
	"context"
	"errors"
	"sync"

	"nvidia-router/internal/runtimeconfig"
)

// Switcher keeps the request path stable while allowing new requests to use a
// newly constructed manager after an administrator changes the proxy config.
type Switcher struct {
	mu      sync.RWMutex
	manager *Manager
	enabled bool
	closed  bool
}

func NewSwitcher(manager *Manager, enabled bool) *Switcher {
	if !enabled {
		manager = nil
	}
	return &Switcher{manager: manager, enabled: enabled}
}

func (s *Switcher) Configured() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled && !s.closed
}

func (s *Switcher) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled && !s.closed
}

func (s *Switcher) Acquire(ctx context.Context, snapshot runtimeconfig.Snapshot, session string) (*Handle, error) {
	if s == nil {
		return nil, errors.New("proxy switcher is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, &Error{reason: ReasonManagerClosed}
	}
	if !s.enabled {
		return nil, errors.New("proxy is disabled")
	}
	if s.manager == nil {
		return nil, &Error{reason: ReasonTransportFailed, cause: errors.New("proxy manager is unavailable")}
	}
	return s.manager.Acquire(ctx, snapshot, session)
}

// Apply atomically publishes a manager. The old manager only closes idle
// connections, so handles already handed to requests keep their transport.
func (s *Switcher) Apply(manager *Manager, enabled bool) error {
	if s == nil {
		if manager != nil {
			manager.Close()
		}
		return errors.New("proxy switcher is nil")
	}
	if !enabled {
		manager = nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if manager != nil {
			manager.Close()
		}
		return errors.New("proxy switcher is closed")
	}
	old := s.manager
	s.manager = manager
	s.enabled = enabled
	s.mu.Unlock()
	if old != nil && old != manager {
		old.Close()
	}
	return nil
}

func (s *Switcher) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	old := s.manager
	s.manager = nil
	s.enabled = false
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
}
