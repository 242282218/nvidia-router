package mocknvidia

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

type SSEChunk struct {
	Data  string
	Delay time.Duration
}

type Script struct {
	Status  int
	Body    string
	Headers http.Header
	Delay   time.Duration
	SSE     []SSEChunk
}

type Request struct {
	Method string
	Path   string
	Header http.Header
}

type Server struct {
	server *httptest.Server

	mu       sync.Mutex
	scripts  []Script
	requests []Request
	canceled int
}

func New(scripts ...Script) *Server {
	server := &Server{scripts: scripts}
	server.server = httptest.NewServer(http.HandlerFunc(server.handle))
	return server
}

func (s *Server) URL() string { return s.server.URL }

func (s *Server) Client() *http.Client { return s.server.Client() }

func (s *Server) Close() { s.server.Close() }

func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *Server) CanceledCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]Request, len(s.requests))
	copy(requests, s.requests)
	return requests
}

func (s *Server) handle(writer http.ResponseWriter, request *http.Request) {
	script := s.next(request)
	for key, values := range script.Headers {
		writer.Header()[key] = append([]string(nil), values...)
	}
	if len(script.SSE) > 0 {
		s.writeSSE(writer, request, script)
		return
	}
	if !waitForRequest(writer, request, script.Delay, s.markCanceled) {
		return
	}
	status := script.Status
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(script.Body))
}

func (s *Server) next(request *http.Request) Script {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method: request.Method,
		Path:   request.URL.Path,
		Header: request.Header.Clone(),
	})
	index := len(s.requests) - 1
	if index >= len(s.scripts) {
		return Script{Status: http.StatusInternalServerError}
	}
	return s.scripts[index]
}

func (s *Server) writeSSE(writer http.ResponseWriter, request *http.Request, script Script) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(statusOrOK(script.Status))
	flusher, _ := writer.(http.Flusher)
	for _, chunk := range script.SSE {
		if !waitForRequest(writer, request, chunk.Delay, s.markCanceled) {
			return
		}
		_, _ = writer.Write([]byte(chunk.Data))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *Server) markCanceled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceled++
}

func waitForRequest(writer http.ResponseWriter, request *http.Request, delay time.Duration, canceled func()) bool {
	if delay == 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-request.Context().Done():
		canceled()
		return false
	}
}

func statusOrOK(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}
