package sse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nvidia-router/internal/router"
)

var ErrStreamInterrupted = errors.New("upstream stream interrupted before [DONE]")

type ProxyOptions struct {
	CommitState *router.CommitState
}

func Proxy(ctx context.Context, writer http.ResponseWriter, upstream *http.Response, opts ProxyOptions) error {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return errors.New("response writer does not support flushing")
	}

	// Close upstream body when the client disconnects or the context is cancelled.
	// request contexts cancel on client close for HTTP/2, and CloseNotify covers
	// the HTTP/1.1 path; watchers race and the loser cleans up harmlessly.
	cancelDone := make(chan struct{})
	defer close(cancelDone)

	var closeNotify <-chan bool
	if notifier, ok := writer.(http.CloseNotifier); ok {
		closeNotify = notifier.CloseNotify()
	}

	go func() {
		select {
		case <-ctx.Done():
			_ = upstream.Body.Close()
		case <-closeNotify:
			_ = upstream.Body.Close()
		case <-cancelDone:
		}
	}()

	decoder := NewDecoder(upstream.Body)
	encoder := NewEncoder(writer)

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	seenDone := false
	firstDataWritten := false
	var pending bytes.Buffer

	for {
		event, err := decoder.Decode()
		if err != nil {
			if err == io.EOF {
				if !seenDone {
					return ErrStreamInterrupted
				}
				return nil
			}
			// Body closed by context cancellation or network error
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("decode SSE event: %w", err)
		}
		if !firstDataWritten && len(event.Data) == 0 {
			var encoded bytes.Buffer
			if err := NewEncoder(&encoded).Encode(event); err != nil {
				return fmt.Errorf("encode pending SSE event: %w", err)
			}
			if encoded.Len() > MaxEventSize-pending.Len() {
				return ErrEventTooLarge
			}
			_, _ = pending.Write(encoded.Bytes())
			continue
		}

		isDone := false
		for _, data := range event.Data {
			trimmed := strings.TrimSpace(data)
			if trimmed == "[DONE]" {
				isDone = true
				break
			}
		}

		if isDone {
			if seenDone {
				continue
			}
			seenDone = true
		}

		if !firstDataWritten {
			commitWriter := writer
			if opts.CommitState != nil {
				commitWriter = opts.CommitState.Wrap(writer)
			}
			commitWriter.WriteHeader(http.StatusOK)
			firstDataWritten = true
			if _, err := io.Copy(commitWriter, bytes.NewReader(pending.Bytes())); err != nil {
				return fmt.Errorf("write pending SSE events: %w", err)
			}
			pending.Reset()
		}

		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("encode SSE event: %w", err)
		}

		flusher.Flush()

		if seenDone {
			return nil
		}
	}
}
