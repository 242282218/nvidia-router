package sse

import (
	"fmt"
	"io"
	"strings"
)

type Encoder struct {
	writer io.Writer
}

func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{writer: w}
}

func (e *Encoder) Encode(event Event) error {
	for _, comment := range event.Comments {
		if _, err := fmt.Fprintf(e.writer, ":%s\n", comment); err != nil {
			return fmt.Errorf("write SSE comment: %w", err)
		}
	}

	if event.Event != "" {
		if _, err := fmt.Fprintf(e.writer, "event: %s\n", event.Event); err != nil {
			return fmt.Errorf("write SSE event: %w", err)
		}
	}

	if event.ID != "" {
		if _, err := fmt.Fprintf(e.writer, "id: %s\n", event.ID); err != nil {
			return fmt.Errorf("write SSE id: %w", err)
		}
	}

	if event.Retry != "" {
		if _, err := fmt.Fprintf(e.writer, "retry: %s\n", event.Retry); err != nil {
			return fmt.Errorf("write SSE retry: %w", err)
		}
	}

	for _, data := range event.Data {
		if _, err := fmt.Fprintf(e.writer, "data: %s\n", data); err != nil {
			return fmt.Errorf("write SSE data: %w", err)
		}
	}

	if _, err := e.writer.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write SSE delimiter: %w", err)
	}

	return nil
}

func (e *Encoder) WriteRaw(line string) error {
	line = strings.TrimSuffix(line, "\n")
	if _, err := fmt.Fprintf(e.writer, "%s\n", line); err != nil {
		return fmt.Errorf("write SSE raw line: %w", err)
	}
	return nil
}
