package router

import (
	"net/http"
	"sync/atomic"
)

type CommitState struct {
	committed atomic.Bool
}

func (s *CommitState) Committed() bool {
	return s.committed.Load()
}

func (s *CommitState) Wrap(writer http.ResponseWriter) http.ResponseWriter {
	wrapped := &commitWriter{ResponseWriter: writer, state: s}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return wrapped
	}
	return &commitFlushingWriter{commitWriter: wrapped, flusher: flusher}
}

type commitWriter struct {
	http.ResponseWriter
	state *CommitState
}

func (w *commitWriter) WriteHeader(statusCode int) {
	w.state.committed.Store(true)
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *commitWriter) Write(payload []byte) (int, error) {
	w.state.committed.Store(true)
	return w.ResponseWriter.Write(payload)
}

func (w *commitWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type commitFlushingWriter struct {
	*commitWriter
	flusher http.Flusher
}

func (w *commitFlushingWriter) Flush() {
	w.state.committed.Store(true)
	w.flusher.Flush()
}
