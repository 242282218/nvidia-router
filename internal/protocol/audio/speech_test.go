package audio

import (
	"bytes"
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func TestParseSpeechRequiresModelAndInput(t *testing.T) {
	tests := []string{
		`{"input":"hello"}`,
		`{"model":"public-tts"}`,
		`{"model":"","input":"hello"}`,
		`{"model":"public-tts","input":""}`,
	}
	for _, payload := range tests {
		if _, err := ParseSpeech([]byte(payload)); err == nil {
			t.Fatalf("ParseSpeech(%s) succeeded", payload)
		}
	}
}

func TestParseSpeechMapsModelAndPreservesVoiceAndFormat(t *testing.T) {
	parsed, err := ParseSpeech([]byte(`{
		"model":"public-tts",
		"input":"do not log this text",
		"voice":"alloy",
		"response_format":"mp3"
	}`))
	if err != nil {
		t.Fatalf("ParseSpeech: %v", err)
	}
	if parsed.PublicModelID() != "public-tts" {
		t.Fatalf("model = %q", parsed.PublicModelID())
	}
	if parsed.Requirements().Kind != modelcatalog.KindTTS {
		t.Fatalf("kind = %q", parsed.Requirements().Kind)
	}

	body, err := parsed.MarshalFor(modelcatalog.Model{UpstreamID: "vendor/tts"})
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	if bytes.Contains(body, []byte("public-tts")) {
		t.Fatalf("public model leaked upstream: %s", body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal mapped body: %v", err)
	}
	assertSpeechString(t, fields, "model", "vendor/tts")
	assertSpeechString(t, fields, "input", "do not log this text")
	assertSpeechString(t, fields, "voice", "alloy")
	assertSpeechString(t, fields, "response_format", "mp3")
}

func TestParseSpeechRejectsNonStringOptionalFields(t *testing.T) {
	for _, payload := range []string{
		`{"model":"public-tts","input":"hello","voice":1}`,
		`{"model":"public-tts","input":"hello","response_format":false}`,
	} {
		if _, err := ParseSpeech([]byte(payload)); err == nil {
			t.Fatalf("ParseSpeech(%s) succeeded", payload)
		}
	}
}

func assertSpeechString(t *testing.T, fields map[string]json.RawMessage, name, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(fields[name], &got); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
