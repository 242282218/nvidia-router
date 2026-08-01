package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWithIdleTimeoutReturnsBodyUnchangedForNonPositiveIdle(t *testing.T) {
	body := io.NopCloser(strings.NewReader("x"))
	if got := WithIdleTimeout(body, 0); got != body {
		t.Fatal("non-positive idle must return the body unchanged")
	}
	if got := WithIdleTimeout(body, -1); got != body {
		t.Fatal("negative idle must return the body unchanged")
	}
}

func TestWithIdleTimeoutReportsStall(t *testing.T) {
	reader, writer := io.Pipe()
	body := WithIdleTimeout(reader, 30*time.Millisecond)
	defer func() { _ = body.Close() }()

	// Pipe writes block until read; run it so the timer cannot close the pipe
	// while this goroutine is still inside Write.
	go func() { _, _ = writer.Write([]byte("first\n")) }()
	buf := make([]byte, 1024)
	if read, err := body.Read(buf); err != nil || read == 0 {
		t.Fatalf("first Read = %d, %v; want data", read, err)
	}

	// With the writer silent the next Read must give up after the idle window
	// instead of blocking forever.
	done := make(chan error, 1)
	go func() {
		_, err := body.Read(buf)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamIdle) {
			t.Fatalf("stalled Read error = %v, want ErrStreamIdle", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled Read did not return within the idle window")
	}
}

func TestWithIdleTimeoutDoesNotInterruptActiveStream(t *testing.T) {
	reader, writer := io.Pipe()
	body := WithIdleTimeout(reader, 50*time.Millisecond)
	defer func() { _ = body.Close() }()

	go func() {
		defer func() { _ = writer.Close() }()
		for index := 0; index < 5; index++ {
			if _, err := writer.Write([]byte("chunk\n")); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	buf := make([]byte, 1024)
	for {
		read, err := body.Read(buf)
		if read > 0 {
			continue
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		t.Fatalf("active stream Read error = %v, want data or EOF", err)
	}
}

func TestWithIdleTimeoutFiresOnlyAfterSilence(t *testing.T) {
	reader, writer := io.Pipe()
	body := WithIdleTimeout(reader, 40*time.Millisecond)
	defer func() { _ = body.Close() }()

	go func() {
		defer func() { _ = writer.Close() }()
		for index := 0; index < 5; index++ {
			if _, err := writer.Write([]byte("chunk\n")); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Silence longer than the idle window to trigger the stall error.
		time.Sleep(120 * time.Millisecond)
	}()

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := body.Read(buf)
			if err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamIdle) {
			t.Fatalf("post-silence Read error = %v, want ErrStreamIdle", err)
		}
	case <-time.After(time.Second):
		t.Fatal("silence did not trigger the idle error")
	}
}
