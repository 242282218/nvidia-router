package nvidia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWithAudioIdleTimeoutReturnsProgressBeforeSilence(t *testing.T) {
	body := newTimedAudioBody([]byte{1}, 1*time.Millisecond)
	wrapped := WithAudioIdleTimeout(body, 20*time.Millisecond)
	defer func() { _ = wrapped.Close() }()

	first := make([]byte, 1)
	if _, err := wrapped.Read(first); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if first[0] != 1 {
		t.Fatalf("first byte = %d, want 1", first[0])
	}
	second := make([]byte, 1)
	_, err := wrapped.Read(second)
	if !errors.Is(err, ErrAudioStreamIdle) {
		t.Fatalf("second read error = %v, want ErrAudioStreamIdle", err)
	}
}

func TestAudioSpeechInstallsIdleTimeoutAfterPriming(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte{1, 2})),
	}
	if err := PrimeAudioSpeech(context.Background(), response); err != nil {
		t.Fatalf("PrimeAudioSpeech: %v", err)
	}
	response.Body = WithAudioIdleTimeout(response.Body, time.Millisecond)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read primed body: %v", err)
	}
	if !bytes.Equal(body, []byte{1, 2}) {
		t.Fatalf("body = %#v", body)
	}
}

func TestWithAudioIdleTimeoutLeavesNonPositiveWindowUnchanged(t *testing.T) {
	body := io.NopCloser(strings.NewReader("audio"))
	if got := WithAudioIdleTimeout(body, 0); got != body {
		t.Fatal("non-positive timeout replaced body")
	}
	_ = body.Close()
}

type timedAudioBody struct {
	payload []byte
	delay   time.Duration
	closed  chan struct{}
}

func newTimedAudioBody(payload []byte, delay time.Duration) *timedAudioBody {
	return &timedAudioBody{payload: payload, delay: delay, closed: make(chan struct{})}
}

func (b *timedAudioBody) Read(dst []byte) (int, error) {
	if len(b.payload) == 0 {
		<-b.closed
		return 0, errors.New("audio body closed")
	}
	timer := time.NewTimer(b.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		count := copy(dst, b.payload)
		b.payload = b.payload[count:]
		return count, nil
	case <-b.closed:
		return 0, errors.New("audio body closed")
	}
}

func (b *timedAudioBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
