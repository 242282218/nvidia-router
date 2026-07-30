package sse

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Prime waits for one complete SSE event before the caller accepts an upstream
// attempt. Every byte read by the decoder is replayed so the proxy sees the
// original stream exactly once, including bytes buffered beyond the first event.
func Prime(ctx context.Context, response *http.Response) error {
	if response == nil || response.Body == nil {
		return io.ErrUnexpectedEOF
	}

	captured := &captureReader{reader: response.Body}
	result := make(chan error, 1)
	go func() {
		_, err := NewDecoder(captured).Decode()
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		result <- err
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
		_, _ = r.Buffer.Write(payload[:read])
	}
	return read, err
}

type replayReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *replayReadCloser) Close() error {
	return r.closer.Close()
}
