package sse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

// MaxCaptureBytes bounds the total bytes Prime buffers while waiting for the
// first data-bearing event. The decoder already caps a single event at
// MaxEventSize, but a stream of comment/keep-alive events before the first data
// event would otherwise grow the capture buffer without bound (checklist #13).
const MaxCaptureBytes = MaxEventSize

// Prime waits for one complete SSE event before the caller accepts an upstream
// attempt. Every byte read by the decoder is replayed so the proxy sees the
// original stream exactly once, including bytes buffered beyond the first event.
func Prime(ctx context.Context, response *http.Response) error {
	if response == nil || response.Body == nil {
		return io.ErrUnexpectedEOF
	}
	if marker, ok := response.Body.(interface{ RequireSemanticCompletion() }); ok {
		marker.RequireSemanticCompletion()
	}

	captured := &captureReader{reader: response.Body}
	result := make(chan error, 1)
	go func() {
		decoder := NewDecoder(captured)
		for {
			event, err := decoder.Decode()
			if errors.Is(err, ErrEventTooLarge) {
				result <- ErrEventTooLarge
				return
			}
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			if err != nil || len(event.Data) > 0 {
				result <- err
				return
			}
		}
	}()

	select {
	case err := <-result:
		if err != nil {
			return err
		}
		response.Body = &replayReadCloser{
			Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), response.Body),
			closer: response.Body,
		}
		return nil
	case <-ctx.Done():
		_ = response.Body.Close()
		<-result
		return ctx.Err()
	}
}

type captureReader struct {
	reader io.Reader
	bytes.Buffer
}

func (r *captureReader) Read(payload []byte) (int, error) {
	read, err := r.reader.Read(payload)
	if read > 0 {
		if r.Len()+read > MaxCaptureBytes {
			// Bound the preamble buffer; Prime surfaces ErrEventTooLarge rather
			// than buffering an unbounded comment/keep-alive preamble. The bytes
			// are consumed but not retained, which is fine: the attempt fails.
			return read, ErrEventTooLarge
		}
		_, _ = r.Write(payload[:read])
	}
	return read, err
}

type replayReadCloser struct {
	io.Reader
	closer io.Closer
}

// MarkComplete forwards semantic stream completion through the replay wrapper
// so proxy quality sees the terminal marker after priming has buffered bytes.
func (r *replayReadCloser) MarkComplete() {
	if marker, ok := r.closer.(interface{ MarkComplete() }); ok {
		marker.MarkComplete()
	}
}

// RequireSemanticCompletion forwards the stream contract through replay.
func (r *replayReadCloser) RequireSemanticCompletion() {
	if marker, ok := r.closer.(interface{ RequireSemanticCompletion() }); ok {
		marker.RequireSemanticCompletion()
	}
}

func (r *replayReadCloser) Close() error {
	return r.closer.Close()
}
