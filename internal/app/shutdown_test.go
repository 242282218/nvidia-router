package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/database"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/runtimeconfig"
)

func TestShutdownMiddlewareRejectsNewAPIRequests(t *testing.T) {
	handler := shutdownMiddleware(func() bool { return true }, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"server_shutting_down"`) {
		t.Fatalf("response = %d %q, want server_shutting_down 503", response.Code, response.Body.String())
	}
}

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

func TestServeAndCloseCanRaceWithoutLeavingHTTPRunning(t *testing.T) {
	server := NewServer("127.0.0.1:0", http.NotFoundHandler(), nil, nil)
	application := &App{Server: server}
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	go func() { serveDone <- application.Serve(serveContext) }()

	closeDone := make(chan error, 1)
	go func() { closeDone <- application.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish while Serve was starting")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve remained active after Close")
	}
}

func TestServeClosesDatabaseWhenListenerFails(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listener: %v", err)
	}
	defer func() { _ = listener.Close() }()

	application := &App{
		Server: NewServer(listener.Addr().String(), http.NotFoundHandler(), nil, nil),
		db:     db,
	}
	if err := application.Serve(context.Background()); err == nil {
		t.Fatal("Serve succeeded while listener address was occupied")
	}
	if err := db.Ping(); err == nil {
		t.Fatal("database remained open after listener failure")
	}
}

func TestShutdownUsesOneDeadlineAcrossAppAndServer(t *testing.T) {
	server := NewServer("127.0.0.1:0", http.NotFoundHandler(), nil, nil)
	application := &App{Server: server}
	application.beginShutdown(100 * time.Millisecond)

	if application.shutdownDeadline.IsZero() {
		t.Fatal("application shutdown deadline was not initialized")
	}
	server.lifecycleMu.Lock()
	serverDeadline := server.shutdownDeadline
	server.lifecycleMu.Unlock()
	if !serverDeadline.Equal(application.shutdownDeadline) {
		t.Fatalf("server deadline = %v, app deadline = %v", serverDeadline, application.shutdownDeadline)
	}
}

func TestAppCloseClosesDatabaseAfterHTTPGraceTimeout(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	settings := &shutdownTestSettings{snapshot: runtimeconfig.Snapshot{ShutdownGraceMS: 20}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	started := make(chan struct{})
	server := NewServer(address, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}), settings, nil)
	application := &App{Server: server, db: db}

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ServeOn(listener, context.Background()) }()
	go func() { _, _ = http.Get("http://" + address) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking request did not start")
	}
	application.beginShutdown(20 * time.Millisecond)
	if err := application.Close(); err == nil {
		t.Fatal("Close succeeded after HTTP grace timeout, want shutdown error")
	}
	if err := db.Ping(); err == nil {
		t.Fatal("database remained open after HTTP grace timeout")
	}
	select {
	case <-serveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after HTTP grace timeout")
	}
}

func TestAppCloseDrainsHTTPBeforeClosingDatabase(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	settings := &shutdownTestSettings{snapshot: runtimeconfig.Snapshot{ShutdownGraceMS: 200}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listener: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release listener: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := shutdownMiddleware(func() bool { return false }, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writer.WriteHeader(http.StatusOK)
	}))
	server := NewServer(address, handler, settings, nil)
	rootContext, rootCancel := context.WithCancel(context.Background())
	application := &App{Server: server, db: db, rootCancel: rootCancel}
	server.setRootContext(rootContext)
	application.beginShutdown(200 * time.Millisecond)

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ListenAndServe(context.Background()) }()
	requestDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			response, requestErr := http.Get("http://" + address + "/v1/models")
			if requestErr == nil {
				_ = response.Body.Close()
				requestDone <- nil
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		requestDone <- errors.New("server did not become reachable")
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- application.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before active request drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if _, err := db.Exec("SELECT 1"); err != nil {
		t.Fatalf("database closed before HTTP drain completed: %v", err)
	}
	select {
	case <-rootContext.Done():
		t.Fatal("root context canceled before active request completed")
	default:
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatalf("active request: %v", err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish")
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	if err := db.Ping(); err == nil {
		t.Fatal("database remained open after Close")
	}
}

func TestAppCloseDrainsRequestRecorderAfterHTTP(t *testing.T) {
	store := &shutdownRecorderStub{}
	recorder := observability.NewBufferRecorder(store, nil, observability.BufferOptions{
		Capacity:   8,
		BatchSize:  8,
		FlushDelay: time.Hour,
	})
	recorderContext, recorderCancel := context.WithCancel(context.Background())
	recorderDone := make(chan struct{})
	go func() {
		recorder.Run(recorderContext)
		close(recorderDone)
	}()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	started := make(chan struct{})
	release := make(chan struct{})
	server := NewServer(listener.Addr().String(), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_ = recorder.Record(context.Background(), observability.RequestRecord{RequestID: "shutdown-request"})
		writer.WriteHeader(http.StatusOK)
	}), nil, nil)
	application := &App{
		Server:          server,
		requestRecorder: recorder,
		recorderCancel:  recorderCancel,
		recorderDone:    recorderDone,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ServeOn(listener, context.Background()) }()
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- application.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before active request drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatalf("active request: %v", err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish")
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("ServeOn: %v", err)
	}
	if got := store.count(); got != 1 {
		t.Fatalf("flushed request records = %d, want 1", got)
	}
}

type shutdownRecorderStub struct {
	mu      sync.Mutex
	records []observability.RequestRecord
}

func (s *shutdownRecorderStub) RecordBatch(_ context.Context, records []observability.RequestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
	return nil
}

func (s *shutdownRecorderStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

type shutdownTestSettings struct {
	snapshot runtimeconfig.Snapshot
}

func (s *shutdownTestSettings) Snapshot() runtimeconfig.Snapshot { return s.snapshot }
