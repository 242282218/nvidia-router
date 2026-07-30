package router

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayableBodySmallStaysInMemory(t *testing.T) {
	payload := []byte("hello world")
	body, err := NewReplayableBody(payload, t.TempDir())
	if err != nil {
		t.Fatalf("NewReplayableBody: %v", err)
	}
	defer body.Close()
	if body.Size() != int64(len(payload)) {
		t.Fatalf("size = %d", body.Size())
	}
	assertReplayReads(t, body, string(payload))
}

func TestReplayableBodyLargeSpillsToFile(t *testing.T) {
	payload := make([]byte, replayMemoryThreshold+1)
	for i := range payload {
		payload[i] = byte(i)
	}
	tempDir := t.TempDir()
	body, err := NewReplayableBody(payload, tempDir)
	if err != nil {
		t.Fatalf("NewReplayableBody: %v", err)
	}
	defer body.Close()
	if body.Size() != int64(len(payload)) {
		t.Fatalf("size = %d", body.Size())
	}
	assertReplayReads(t, body, string(payload))
	// File-backed body must live under the configured temp dir.
	if rel, err := filepath.Rel(tempDir, filePath(body)); err != nil || rel == ".." || rel[:2] == ".." {
		t.Fatalf("temp file not under tempDir: %s", filePath(body))
	}
}

func TestReplayableBodyReopenable(t *testing.T) {
	payload := make([]byte, replayMemoryThreshold+1)
	body, err := NewReplayableBody(payload, t.TempDir())
	if err != nil {
		t.Fatalf("NewReplayableBody: %v", err)
	}
	defer body.Close()
	for i := 0; i < 3; i++ {
		assertReplayReads(t, body, string(payload))
	}
}

func TestReplayableBodyRejectsOversized(t *testing.T) {
	payload := make([]byte, maxReplayBytes+1)
	_, err := NewReplayableBody(payload, t.TempDir())
	if !BodyTooLarge(err) {
		t.Fatalf("error = %v, want BodyTooLarge", err)
	}
}

func TestReplayableBodyCloseIsIdempotentAndCleansFile(t *testing.T) {
	payload := make([]byte, replayMemoryThreshold+1)
	tempDir := t.TempDir()
	body, err := NewReplayableBody(payload, tempDir)
	if err != nil {
		t.Fatalf("NewReplayableBody: %v", err)
	}
	path := filePath(body)
	if err := body.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// File must be gone after Close.
	if _, statErr := osStat(path); !errors.Is(statErr, errNotExist()) {
		t.Fatalf("file still present after close: %v", statErr)
	}
	// Second close must not error.
	if err := body.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestReplayableBodyRemovesFileOnCloseAfterOpen(t *testing.T) {
	payload := make([]byte, replayMemoryThreshold+1)
	body, err := NewReplayableBody(payload, t.TempDir())
	if err != nil {
		t.Fatalf("NewReplayableBody: %v", err)
	}
	// Open a reader, then close the body; the file must still be removed.
	reader, err := body.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, _ = io.ReadAll(reader)
	_ = reader.Close()
	path := filePath(body)
	if err := body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, statErr := osStat(path); !errors.Is(statErr, errNotExist()) {
		t.Fatalf("file still present after close: %v", statErr)
	}
}

func TestReplayableBodyRequiresTempDirForFileBacked(t *testing.T) {
	payload := make([]byte, replayMemoryThreshold+1)
	if _, err := NewReplayableBody(payload, ""); err == nil {
		t.Fatal("expected error for empty temp dir with file-backed body")
	}
}

func assertReplayReads(t *testing.T, body ReplayableBody, want string) {
	t.Helper()
	reader, err := body.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = reader.Close()
	if string(got) != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}

// filePath extracts the backing path from a fileReplayBody via Open, so tests
// can assert cleanup. Memory-backed bodies return "".
func filePath(body ReplayableBody) string {
	fb, ok := body.(*fileReplayBody)
	if !ok {
		return ""
	}
	return fb.path
}

func osStat(path string) (os.FileInfo, error) { return os.Stat(path) }

func errNotExist() error { return os.ErrNotExist }
