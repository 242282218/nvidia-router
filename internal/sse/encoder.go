package sse

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
)

var eventBufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

type Encoder struct {
	writer io.Writer
}

func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{writer: w}
}

func (e *Encoder) Encode(event Event) error {
	buf := eventBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer eventBufPool.Put(buf)

	for _, comment := range event.Comments {
		buf.WriteByte(':')
		buf.WriteString(comment)
		buf.WriteByte('\n')
	}

	if event.Event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event.Event)
		buf.WriteByte('\n')
	}

	if event.ID != "" {
		buf.WriteString("id: ")
		buf.WriteString(event.ID)
		buf.WriteByte('\n')
	}

	if event.Retry != "" {
		buf.WriteString("retry: ")
		buf.WriteString(event.Retry)
		buf.WriteByte('\n')
	}

	for _, data := range event.Data {
		// Per SSE spec a data field must not contain raw newlines. A raw
		// newline inside one data value would be parsed by the client as a
		// new field line (event boundary confusion / injection). Split on
		// any newline sequence and emit each segment as its own data: line.
		normalized := strings.ReplaceAll(data, "\r\n", "\n")
		normalized = strings.ReplaceAll(normalized, "\r", "\n")
		if strings.Contains(normalized, "\n") {
			for _, part := range strings.Split(normalized, "\n") {
				buf.WriteString("data: ")
				buf.WriteString(part)
				buf.WriteByte('\n')
			}
			continue
		}
		buf.WriteString("data: ")
		buf.WriteString(data)
		buf.WriteByte('\n')
	}

	buf.WriteByte('\n')

	if _, err := e.writer.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write SSE event: %w", err)
	}

	return nil
}

func (e *Encoder) WriteRaw(line string) error {
	line = strings.TrimSuffix(line, "\n")
	buf := eventBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer eventBufPool.Put(buf)

	buf.WriteString(line)
	buf.WriteByte('\n')

	if _, err := e.writer.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write SSE raw line: %w", err)
	}
	return nil
}
