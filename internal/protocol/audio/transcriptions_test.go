package audio

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/internal/modelcatalog"
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

func TestParseMultipartRejectsEmptyFile(t *testing.T) {
	body, contentType := multipartBody("public-asr", map[string][]byte{"file": {}})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	request.Header.Set("Content-Type", contentType)
	if _, err := ParseMultipart(request); err == nil {
		t.Fatal("expected empty file rejection")
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
	if !strings.Contains(outCT, "multipart/form-data") {
		t.Fatalf("content-type = %q", outCT)
	}
	// Model must be mapped; unmapped user field must be dropped; file forwarded.
	if !bytes.Contains(out, []byte("vendor/asr")) {
		t.Fatalf("mapped model missing: %s", out)
	}
	if bytes.Contains(out, []byte("public-asr")) {
		t.Fatalf("public model leaked upstream: %s", out)
	}
	if !bytes.Contains(out, payload) {
		t.Fatalf("file bytes missing from upstream body")
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
