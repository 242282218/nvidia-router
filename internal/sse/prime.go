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

var ErrNoSemanticData = errors.New("SSE stream ended without semantic output")

// Prime waits for one complete SSE event before the caller accepts an upstream
// attempt. Every byte read by the decoder is replayed so the proxy sees the
// original stream exactly once, including bytes buffered beyond the first event.
func Prime(ctx context.Context, response *http.Response) error {
	return PrimeUntil(ctx, response, func(event Event) (bool, error) {
		return len(event.Data) > 0, nil
	})
}

func PrimeUntil(ctx context.Context, response *http.Response, accept func(Event) (bool, error)) error {
	if response == nil || response.Body == nil {
		return io.ErrUnexpectedEOF
	}
	if marker, ok := response.Body.(interface{ RequireSemanticCompletion() }); ok {
		marker.RequireSemanticCompletion()
	}

	captured := &captureReader{reader: response.Body}
	// Cancel-aware decode without per-stream goroutine: AfterFunc closes the
	// body when ctx is cancelled, unblocking Decode's Read (which then returns
	// error and we map it to ctx.Err). Saves 1 goroutine + channel per SSE
	// stream (1k streams = 1k goroutines saved).
	stop := context.AfterFunc(ctx, func() { _ = response.Body.Close() })
	defer stop()

	decoder := NewDecoder(captured)
	for {
		event, err := decoder.Decode()
		if errors.Is(err, ErrEventTooLarge) {
			return ErrEventTooLarge
		}
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		accepted, acceptErr := accept(event)
		if acceptErr != nil {
			return acceptErr
		}
		if accepted {
			response.Body = &replayReadCloser{
				Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), response.Body),
				closer: response.Body,
			}
			return nil
		}
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
