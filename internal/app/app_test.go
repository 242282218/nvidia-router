package app

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestNew(t *testing.T) {
	app, err := New(context.Background(), Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app == nil {
		t.Fatal("expected app")
	}
}

func TestRunCLIPropagatesUsageWriteError(t *testing.T) {
	writeErr := errors.New("write usage")

	err := runCLI([]string{"--help"}, errorWriter{err: writeErr}, io.Discard)

	if !errors.Is(err, writeErr) {
		t.Fatalf("runCLI error = %v, want %v", err, writeErr)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
