package app

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/pool"
)

func TestBeginShutdownCancelsRootAfterGrace(t *testing.T) {
	rootContext, rootCancel := context.WithCancel(context.Background())
	application := &App{rootCancel: rootCancel}

	application.beginShutdown(20 * time.Millisecond)
	select {
	case <-rootContext.Done():
		t.Fatal("root context canceled before grace period")
	case <-time.After(5 * time.Millisecond):
	}
	select {
	case <-rootContext.Done():
	case <-time.After(time.Second):
		t.Fatal("root context was not canceled after grace period")
	}
}

func TestCloseRejectsNewPoolAcquires(t *testing.T) {
	keyPool := pool.New(nil, nil)
	application := &App{Pool: keyPool}

	application.beginShutdown(time.Second)
	_, err := keyPool.Acquire(context.Background(), 1, nil)
	var publicError *apierror.Error
	if !errors.As(err, &publicError) || publicError.Status != http.StatusServiceUnavailable || publicError.Code != "server_shutting_down" {
		t.Fatalf("Acquire error = %v, want server_shutting_down", err)
	}
	if !application.shutting.Load() {
		t.Fatal("application did not report shutting down")
	}
}
