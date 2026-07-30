package mocknvidia

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

type SSEChunk struct {
	Data        string
	Delay       time.Duration
	NoKeepAlive bool
}

type Script struct {
	Status       int
	Body         string
	Headers      http.Header
	Delay        time.Duration
	BodyDelay    time.Duration
	Disconnect   bool
	FlushHeaders bool
	SSE          []SSEChunk
}

type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type Server struct {
	server *httptest.Server

	mu        sync.Mutex
	scripts   []Script
	requests  []Request
	canceled  int
	active    int
	maxActive int
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

func (s *Server) MaxActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
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
	s.beginRequest()
	defer s.endRequest()
	script := s.next(request)
	if script.Disconnect {
		s.disconnect(writer)
		return
	}
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
	if script.FlushHeaders {
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if !waitForRequest(writer, request, script.BodyDelay, s.markCanceled) {
		return
	}
	_, _ = writer.Write([]byte(script.Body))
}

func (s *Server) disconnect(writer http.ResponseWriter) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_ = connection.Close()
}

func (s *Server) next(request *http.Request) Script {
	body, _ := io.ReadAll(request.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method: request.Method,
		Path:   request.URL.Path,
		Header: request.Header.Clone(),
		Body:   append([]byte(nil), body...),
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
	if script.FlushHeaders && flusher != nil {
		flusher.Flush()
	}
	for _, chunk := range script.SSE {
		if !waitForStreamDelay(writer, request, chunk.Delay, chunk.NoKeepAlive, flusher, s.markCanceled) {
			return
		}
		if chunk.Data != "" {
			if _, err := writer.Write([]byte(chunk.Data)); err != nil {
				s.markCanceled()
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) beginRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
}

func (s *Server) endRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
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

// waitForStreamDelay sleeps before emitting an SSE chunk. Because httptest does
// not cancel an in-flight HTTP/1.1 streaming request when the client disconnects,
// we probe the connection with comment keepalives: a write failure proves the
// client is gone and counts as a cancellation.
func waitForStreamDelay(writer http.ResponseWriter, request *http.Request, delay time.Duration, noKeepAlive bool, flusher http.Flusher, canceled func()) bool {
	if delay == 0 {
		return true
	}
	deadline := time.NewTimer(delay)
	defer deadline.Stop()
	if noKeepAlive {
		select {
		case <-deadline.C:
			return true
		case <-request.Context().Done():
			canceled()
			return false
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return true
		case <-request.Context().Done():
			canceled()
			return false
		case <-ticker.C:
			if _, err := writer.Write([]byte(": keepalive\n\n")); err != nil {
				canceled()
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func statusOrOK(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}
