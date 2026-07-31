package nvidia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/runtimeconfig"
)

func TestAudioTranscriptionsSendsMultipart(t *testing.T) {
	var captured struct {
		auth, contentType, method string
		body                      []byte
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured.method = request.Method
		captured.auth = request.Header.Get("Authorization")
		captured.contentType = request.Header.Get("Content-Type")
		captured.body, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"text":"hello world"}`))
	}))
	t.Cleanup(upstream.Close)

	descriptor := DefaultDescriptor()
	descriptor.ASR.URL = upstream.URL + "/v1/audio/transcriptions"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	body := []byte("--bound\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n\r\nWAVDATA\r\n--bound--")
	resp, err := client.AudioTranscriptions(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 10000}, "nvapi-secret", body, "multipart/form-data; boundary=bound")
	if err != nil {
		t.Fatalf("AudioTranscriptions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if captured.method != http.MethodPost {
		t.Fatalf("method = %q", captured.method)
	}
	if captured.auth != "Bearer nvapi-secret" {
		t.Fatalf("auth = %q", captured.auth)
	}
	if !strings.HasPrefix(captured.contentType, "multipart/form-data") {
		t.Fatalf("content-type = %q", captured.contentType)
	}
	if !bytes.Equal(captured.body, body) {
		t.Fatalf("body = %q", captured.body)
	}
}

func TestValidateNonstreamAudioRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"not json":        `hello`,
		"no text":         `{"usage":{}}`,
		"text not string": `{"text":123}`,
	}
	for name, body := range cases {
		resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
		if _, err := ValidateNonstreamAudio(resp); err == nil {
			t.Fatalf("%s: expected protocol error", name)
		}
	}
}

func TestValidateNonstreamAudioAcceptsTextOrTranscript(t *testing.T) {
	cases := map[string]string{
		"text":       `{"text":"hi"}`,
		"transcript": `{"transcript":"hi"}`,
	}
	for name, body := range cases {
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"r1"}}, Body: io.NopCloser(strings.NewReader(body))}
		validated, err := ValidateNonstreamAudio(resp)
		if err != nil {
			t.Fatalf("%s: Validate: %v", name, err)
		}
		if validated.Metadata.RequestID != "r1" {
			t.Fatalf("%s: request id = %q", name, validated.Metadata.RequestID)
		}
	}
}

func TestValidateNonstreamAudioRequiresNonEmptyTranscript(t *testing.T) {
	for _, body := range []string{
		`{"text":""}`,
		`{"text":"  "}`,
		`{"transcript":""}`,
		`{"transcript":"\t"}`,
		`{}`,
	} {
		response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		if _, err := ValidateNonstreamAudio(response); !errors.Is(err, ErrProtocol) {
			t.Fatalf("body %s error = %v, want protocol error", body, err)
		}
	}
}

func TestAudioSpeechSendsJSON(t *testing.T) {
	var captured struct {
		auth, contentType, method string
		body                      []byte
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured.method = request.Method
		captured.auth = request.Header.Get("Authorization")
		captured.contentType = request.Header.Get("Content-Type")
		captured.body, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "audio/mpeg")
		_, _ = writer.Write([]byte{0x01, 0x02})
	}))
	t.Cleanup(upstream.Close)

	descriptor := DefaultDescriptor()
	descriptor.TTS.URL = upstream.URL + "/v1/audio/speech"
	client, err := NewClient(upstream.Client(), descriptor, fixedSettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	body := []byte(`{"model":"vendor/tts","input":"hello","voice":"alloy"}`)
	response, err := client.AudioSpeech(context.Background(), runtimeconfig.Snapshot{ConnectTimeoutMS: 10000}, "nvapi-secret", body)
	if err != nil {
		t.Fatalf("AudioSpeech: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if captured.method != http.MethodPost || captured.auth != "Bearer nvapi-secret" {
		t.Fatalf("request = %q %q", captured.method, captured.auth)
	}
	if captured.contentType != "application/json" {
		t.Fatalf("content-type = %q", captured.contentType)
	}
	if !bytes.Equal(captured.body, body) {
		t.Fatalf("body = %s", captured.body)
	}
}

func TestPrimeAudioSpeechRequiresFirstByteAndPreservesBody(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte{1, 2, 3, 4}))}
	if err := PrimeAudioSpeech(context.Background(), response); err != nil {
		t.Fatalf("PrimeAudioSpeech: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read primed body: %v", err)
	}
	if !bytes.Equal(body, []byte{1, 2, 3, 4}) {
		t.Fatalf("body = %#v", body)
	}

	empty := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}
	if err := PrimeAudioSpeech(context.Background(), empty); !errors.Is(err, ErrProtocol) {
		t.Fatalf("empty error = %v", err)
	}
}

func TestPrimeAudioSpeechHonorsFirstByteContext(t *testing.T) {
	body := newBlockingAudioBody()
	response := &http.Response{StatusCode: http.StatusOK, Body: body}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := PrimeAudioSpeech(ctx, response)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if !body.closed {
		t.Fatal("timed out response body was not closed")
	}
}

type blockingAudioBody struct {
	done   chan struct{}
	closed bool
}

func newBlockingAudioBody() *blockingAudioBody {
	return &blockingAudioBody{done: make(chan struct{})}
}

func (b *blockingAudioBody) Read([]byte) (int, error) {
	<-b.done
	return 0, errors.New("body closed")
}

func (b *blockingAudioBody) Close() error {
	if !b.closed {
		b.closed = true
		close(b.done)
	}
	return nil
}
