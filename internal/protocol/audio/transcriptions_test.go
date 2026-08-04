package audio

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
)

func TestParseMultipartRequiresModelAndFile(t *testing.T) {
	body, contentType := multipartBody("", nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if _, err := ParseMultipart(request); err == nil {
		t.Fatal("expected missing model rejection")
	}

	body, contentType = multipartBody("public-asr", nil)
	request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if _, err := ParseMultipart(request); err == nil {
		t.Fatal("expected missing file rejection")
	}
}

func TestParseMultipartAcceptsFileAndPreservesFields(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	body, contentType := multipartBody("public-asr", map[string][]byte{
		"file":     payload,
		"language": []byte("en"),
		"prompt":   []byte("transcribe"),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	parsed, err := ParseMultipart(request)
	if err != nil {
		t.Fatalf("ParseMultipart: %v", err)
	}
	if parsed.ModelID() != "public-asr" {
		t.Fatalf("model = %q", parsed.ModelID())
	}
	if !bytes.Equal(parsed.FileBytes(), payload) {
		t.Fatalf("file bytes = %#v", parsed.FileBytes())
	}
	if parsed.Requirements().Kind != modelcatalog.KindASR {
		t.Fatalf("kind = %q", parsed.Requirements().Kind)
	}
	if parsed.language != "en" || parsed.prompt != "transcribe" {
		t.Fatalf("language/prompt = %q/%q", parsed.language, parsed.prompt)
	}
}

func TestParseMultipartRejectsNonJSONResponseFormats(t *testing.T) {
	for _, format := range []string{"srt", "vtt", "text"} {
		body, contentType := multipartBody("public-asr", map[string][]byte{
			"file":            {0x01, 0x02, 0x03, 0x04},
			"response_format": []byte(format),
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
		request.Header.Set("Content-Type", contentType)
		_, err := ParseMultipart(request)
		if err == nil {
			t.Fatalf("response_format %q: expected rejection", format)
		}
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("response_format %q: error type %T, want *apierror.Error", format, err)
		}
		if apiErr.Status != http.StatusBadRequest || apiErr.Code != "unsupported_response_format" {
			t.Fatalf("response_format %q: status/code = %d/%q, want 400/unsupported_response_format",
				format, apiErr.Status, apiErr.Code)
		}
	}
}

func TestParseMultipartExposesSupportedResponseFormat(t *testing.T) {
	body, contentType := multipartBody("public-asr", map[string][]byte{
		"file":            {0x01, 0x02, 0x03, 0x04},
		"response_format": []byte("verbose_json"),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	parsed, err := ParseMultipart(request)
	if err != nil {
		t.Fatalf("ParseMultipart: %v", err)
	}
	defer func() { _ = parsed.Close() }()
	if parsed.ResponseFormat() != "verbose_json" {
		t.Fatalf("ResponseFormat = %q", parsed.ResponseFormat())
	}
}

func TestParseMultipartRejectsBodyLargerThanLimitEvenWithSmallFile(t *testing.T) {
	body, contentType := multipartBody("public-asr", map[string][]byte{
		"file":   {0x01},
		"prompt": []byte(strings.Repeat("x", MaxAudioBodyBytes)),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if _, err := ParseMultipart(request); err == nil {
		t.Fatal("expected whole multipart body limit rejection")
	}
}

func TestParseMultipartSpillsLargeFileToReplayStorage(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, (1<<20)+1)
	body, contentType := multipartBody("public-asr", map[string][]byte{"file": payload})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	parsed, err := ParseMultipart(request)
	if err != nil {
		t.Fatalf("ParseMultipart: %v", err)
	}
	defer func() { _ = parsed.Close() }()
	if got := parsed.FileSize(); got != int64(len(payload)) {
		t.Fatalf("file size = %d, want %d", got, len(payload))
	}
}

func TestParseMultipartUsesConfiguredTempDirForLargeFile(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, (1<<20)+1)
	tempDir := t.TempDir()
	body, contentType := multipartBody("public-asr", map[string][]byte{"file": payload})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	parsed, err := ParseMultipart(request, tempDir)
	if err != nil {
		t.Fatalf("ParseMultipart: %v", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read configured temp dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("configured temp dir has no replay file")
	}
	if err := parsed.Close(); err != nil {
		t.Fatalf("close parsed request: %v", err)
	}
	entries, err = os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read configured temp dir after close: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("configured temp dir retains %d files after close", len(entries))
	}
}

func TestParseMultipartRejectsEmptyFile(t *testing.T) {
	body, contentType := multipartBody("public-asr", map[string][]byte{"file": {}})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if _, err := ParseMultipart(request); err == nil {
		t.Fatal("expected empty file rejection")
	}
}

func TestParseMultipartRejectsMultipleFileParts(t *testing.T) {
	body, contentType := multipartBodyFiles("public-asr", [][]byte{{1}, {2}})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if _, err := ParseMultipart(request); err == nil {
		t.Fatal("expected multiple file parts rejection")
	}
}

func TestEncodeUpstreamMapsModelAndPreservesFields(t *testing.T) {
	payload := []byte{0x0A, 0x0B, 0x0C}
	body, contentType := multipartBody("public-asr", map[string][]byte{
		"file":     payload,
		"language": []byte("en"),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	parsed, err := ParseMultipart(request)
	if err != nil {
		t.Fatalf("ParseMultipart: %v", err)
	}
	out, outCT, err := parsed.EncodeUpstream("vendor/asr")
	if err != nil {
		t.Fatalf("EncodeUpstream: %v", err)
	}
	defer func() { _ = out.Close() }()
	encoded, err := out.Open()
	if err != nil {
		t.Fatalf("open encoded body: %v", err)
	}
	encodedPayload, err := io.ReadAll(encoded)
	_ = encoded.Close()
	if err != nil {
		t.Fatalf("read encoded body: %v", err)
	}
	if !strings.Contains(outCT, "multipart/form-data") {
		t.Fatalf("content-type = %q", outCT)
	}
	// Model must be mapped; unmapped user field must be dropped; file forwarded.
	if !bytes.Contains(encodedPayload, []byte("vendor/asr")) {
		t.Fatalf("mapped model missing: %s", encodedPayload)
	}
	if bytes.Contains(encodedPayload, []byte("public-asr")) {
		t.Fatalf("public model leaked upstream: %s", encodedPayload)
	}
	if !bytes.Contains(encodedPayload, []byte{0x0A, 0x0B, 0x0C}) {
		t.Fatalf("file bytes missing from upstream body")
	}
}

func TestEncodeUpstreamReleasesSourceReplayStorage(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, (1<<20)+1)
	tempDir := t.TempDir()
	body, contentType := multipartBody("public-asr", map[string][]byte{"file": payload})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	parsed, err := ParseMultipart(request, tempDir)
	if err != nil {
		t.Fatalf("ParseMultipart: %v", err)
	}
	defer func() { _ = parsed.Close() }()

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir after parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("replay files after parse = %d, want 1", len(entries))
	}
	upstream, _, err := parsed.EncodeUpstream("vendor/asr")
	if err != nil {
		t.Fatalf("EncodeUpstream: %v", err)
	}
	defer func() { _ = upstream.Close() }()

	entries, err = os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir after encode: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("replay files after encode = %d, want 1 source file released", len(entries))
	}
}

// TestEncodeUpstreamRejectsRebuiltBodyOverReplayLimit proves the replay-overflow
// sentinel from router.NewReplayableBody propagates out of EncodeUpstream so
// the HTTP layer can map it to 413. ParseMultipart caps inbound at
// MaxAudioBodyBytes (~25 MiB), so with the current config a rebuilt body is
// bounded by inbound cap == replay limit and the overflow case stays latent.
// The contract still matters: EncodeUpstream must not hide the sentinel (e.g.
// by wrapping it into a 500-class error), and any future widening of the
// local inbound cap revives this path. We bypass ParseMultipart and construct
// a Request with a file body sized exactly at MaxAudioBodyBytes plus minimal
// multipart overhead, which EncodeUpstream's rebuilt body exceeds.
func TestEncodeUpstreamRejectsRebuiltBodyOverReplayLimit(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, MaxAudioBodyBytes)
	fileBody, err := router.NewReplayableBody(payload, t.TempDir())
	if err != nil {
		t.Fatalf("NewReplayableBody: %v", err)
	}
	defer func() { _ = fileBody.Close() }()
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	header.Set("Content-Type", "audio/wav")
	form := &multipart.Form{Value: map[string][]string{
		"model":           {"public-asr"},
		"language":        {"en"},
		"response_format": {"json"},
	}}
	req := Request{
		file:        &multipart.FileHeader{Filename: "audio.wav", Header: header, Size: int64(len(payload))},
		fileBody:    fileBody,
		form:        form,
		model:       "public-asr",
		language:    "en",
		responseFmt: "json",
	}
	_, _, err = req.EncodeUpstream("vendor/asr")
	if err == nil {
		t.Fatal("EncodeUpstream succeeded; expected replay-overflow sentinel")
	}
	// EncodeUpstream wraps the sentinel under a "capture upstream multipart
	// body: %w" frame. BodyTooLarge must still see through it.
	if !router.BodyTooLarge(err) {
		t.Fatalf("expected router.BodyTooLarge sentinel, got %v", err)
	}
}

func multipartBody(model string, parts map[string][]byte) (body *bytes.Buffer, contentType string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if model != "" {
		_ = writer.WriteField("model", model)
	}
	for name, data := range parts {
		if name == "file" {
			part, _ := writer.CreateFormFile("file", "audio.wav")
			_, _ = part.Write(data)
		} else {
			_ = writer.WriteField(name, string(data))
		}
	}
	_ = writer.Close()
	return &buf, writer.FormDataContentType()
}

func multipartBodyFiles(model string, files [][]byte) (body *bytes.Buffer, contentType string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", model)
	for _, data := range files {
		part, _ := writer.CreateFormFile("file", "audio.wav")
		_, _ = part.Write(data)
	}
	_ = writer.Close()
	return &buf, writer.FormDataContentType()
}
