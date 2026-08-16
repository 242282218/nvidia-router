package router

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// Thresholds for the replay buffer. Small payloads stay in memory; larger
// ones spill to a temporary file; anything beyond maxReplayBytes is rejected.
const (
	replayMemoryThreshold = 1 << 20
	maxReplayBytes        = 25 << 20
)

// ReplayableBody is a request body that can be read multiple times so the
// Attempt loop can replay it against a different NVIDIA key after a failover.
// Implementations never persist contents: memory-backed ones hold a byte slice
// for the request lifetime, file-backed ones delete the temp file on Close.
type ReplayableBody interface {
	// Open returns a fresh reader positioned at the start of the contents.
	Open() (io.ReadCloser, error)
	// Size reports the number of bytes that will be read.
	Size() int64
	// Close releases any backing resources. It is idempotent.
	Close() error
}

// NewReplayableBody captures payload for repeated reads. If the payload exceeds
// maxReplayBytes it is rejected with a request-too-large error; if tempDir is
// empty or the file cannot be created, an error is returned. The caller is
// responsible for calling Close exactly once when the request finishes.
func NewReplayableBody(payload []byte, tempDir string) (ReplayableBody, error) {
	return NewReplayableBodyFromReader(bytes.NewReader(payload), int64(len(payload)), tempDir)
}

// NewReplayableBodyFromReader captures a bounded reader without first loading a
// large payload into memory. Small bodies stay in memory; larger bodies are
// streamed to a 0600 temporary file.
func NewReplayableBodyFromReader(reader io.Reader, size int64, tempDir string) (ReplayableBody, error) {
	if size < 0 || size > maxReplayBytes {
		return nil, fmt.Errorf("capture replayable body: %w", errBodyTooLarge)
	}
	if size <= replayMemoryThreshold {
		payload, err := io.ReadAll(io.LimitReader(reader, size+1))
		if err != nil {
			return nil, fmt.Errorf("read replayable body: %w", err)
		}
		if int64(len(payload)) != size {
			return nil, fmt.Errorf("read replayable body: expected %d bytes, got %d", size, len(payload))
		}
		return &memoryReplayBody{payload: payload}, nil
	}
	return newFileReplayBodyFromReader(reader, size, tempDir)
}

// CaptureStreamedReplay writes an unknown-size body to a temp file as it is
// produced and returns a ReplayableBody backed by that file. Unlike
// NewReplayableBodyFromReader it does not require the final size in advance, so
// the caller can stream a freshly assembled multipart body (whose length only
// becomes known after the writer closes) without buffering the whole thing in
// memory. The caller-provided produce callback receives an io.Writer and must
// report how many bytes it wrote plus an optional aux value (e.g. the multipart
// boundary it used). Exceeding maxReplayBytes is rejected with the same sentinel
// so HTTP layers map it to 413. An empty tempDir falls back to the OS default.
func CaptureStreamedReplay[T any](tempDir string, produce func(writer io.Writer) (written int64, aux T, err error)) (ReplayableBody, T, error) {
	var zero T
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	file, err := os.CreateTemp(tempDir, "nvreplay-*.bin")
	if err != nil {
		return nil, zero, fmt.Errorf("create replay temp file: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("set replay temp file permissions: %w", err)
	}
	written, aux, err := produce(file)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("write replay stream: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("close replay temp file: %w", err)
	}
	if written < 0 {
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("capture replayable body: %w", errBodyTooLarge)
	}
	info, err := os.Stat(file.Name())
	if err != nil {
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("stat replay temp file: %w", err)
	}
	if info.Size() > maxReplayBytes {
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("capture replayable body: %w", errBodyTooLarge)
	}
	if info.Size() != written {
		_ = os.Remove(file.Name())
		return nil, zero, fmt.Errorf("capture replayable body: callback reported %d bytes, file contains %d", written, info.Size())
	}
	return &fileReplayBody{path: file.Name(), size: info.Size()}, aux, nil
}

// BodyTooLarge reports whether err is the sentinel returned for payloads that
// exceed the replay limit, so HTTP layers can map it to 413.
func BodyTooLarge(err error) bool {
	return errors.Is(err, errBodyTooLarge)
}

var errBodyTooLarge = errors.New("request body exceeds replay limit of 25 MiB")

// memoryReplayBody holds the payload in memory and hands out independent slices
// on each Open so concurrent replays stay safe.
type memoryReplayBody struct {
	payload []byte
}

func (b *memoryReplayBody) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.payload)), nil
}

func (b *memoryReplayBody) Size() int64 { return int64(len(b.payload)) }

func (b *memoryReplayBody) Close() error { return nil }

// fileReplayBody spills the payload to a temp file so large audio bodies do not
// pin RAM across slow upstream attempts. The file is created with mode 0600 and
// removed on Close.
type fileReplayBody struct {
	path string
	size int64
}

func newFileReplayBodyFromReader(reader io.Reader, size int64, tempDir string) (*fileReplayBody, error) {
	if tempDir == "" {
		return nil, errors.New("capture replayable body: temp dir is required for file-backed body")
	}
	file, err := os.CreateTemp(tempDir, "nvreplay-*.bin")
	if err != nil {
		return nil, fmt.Errorf("create replay temp file: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("set replay temp file permissions: %w", err)
	}
	written, err := io.Copy(file, io.LimitReader(reader, size+1))
	if err != nil || written != size {
		_ = file.Close()
		_ = os.Remove(file.Name())
		if err != nil {
			return nil, fmt.Errorf("write replay temp file: %w", err)
		}
		return nil, fmt.Errorf("write replay temp file: expected %d bytes, wrote %d", size, written)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("close replay temp file: %w", err)
	}
	return &fileReplayBody{path: file.Name(), size: written}, nil
}

func (b *fileReplayBody) Open() (io.ReadCloser, error) {
	file, err := os.Open(b.path)
	if err != nil {
		return nil, fmt.Errorf("open replay temp file: %w", err)
	}
	return file, nil
}

func (b *fileReplayBody) Size() int64 { return b.size }

// Close removes the backing file. After the first call it becomes a no-op so
// repeated releases (e.g. deferred cleanup plus explicit cleanup) stay safe.
func (b *fileReplayBody) Close() error {
	if b.path == "" {
		return nil
	}
	err := os.Remove(b.path)
	b.path = ""
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove replay temp file: %w", err)
	}
	return nil
}
