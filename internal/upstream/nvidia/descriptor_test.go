package nvidia

import (
	"net/http"
	"net/url"
	"testing"
)

func TestDescriptorDefaultEndpointsAndHeaders(t *testing.T) {
	descriptor := DefaultDescriptor()
	tests := []struct {
		name     string
		endpoint Endpoint
		method   string
		url      string
		content  string
		stream   bool
	}{
		{"models", descriptor.Models, http.MethodGet, "https://integrate.api.nvidia.com/v1/models", "", false},
		{"chat", descriptor.Chat, http.MethodPost, "https://integrate.api.nvidia.com/v1/chat/completions", "application/json", false},
		{"chat stream", descriptor.Chat, http.MethodPost, "https://integrate.api.nvidia.com/v1/chat/completions", "application/json", true},
		{"embedding", descriptor.Embedding, http.MethodPost, "https://integrate.api.nvidia.com/v1/embeddings", "application/json", false},
		{"asr", descriptor.ASR, http.MethodPost, "https://integrate.api.nvidia.com/v1/audio/transcriptions", "multipart/form-data", false},
		{"tts", descriptor.TTS, http.MethodPost, "https://integrate.api.nvidia.com/v1/audio/speech", "application/json", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := descriptor.NewRequest(test.endpoint, test.stream, "test-token")
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if request.Method != test.method || request.URL.String() != test.url {
				t.Fatalf("request = %s %s", request.Method, request.URL)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := request.Header.Get("Content-Type"); got != test.content {
				t.Fatalf("Content-Type = %q, want %q", got, test.content)
			}
			wantAccept := ""
			if test.stream {
				wantAccept = "text/event-stream"
			}
			if got := request.Header.Get("Accept"); got != wantAccept {
				t.Fatalf("Accept = %q, want %q", got, wantAccept)
			}
		})
	}
}

func TestDescriptorCapabilityHintsAreConservative(t *testing.T) {
	descriptor := DefaultDescriptor()
	unknown := descriptor.CapabilityHint("unknown/model")
	if unknown != (CapabilityHint{Kind: KindChat, ReasoningWireFormat: ReasoningWireNone}) {
		t.Fatalf("unknown hint = %#v", unknown)
	}

	for _, modelID := range []string{
		"minimaxai/minimax-m2.7",
		"minimaxai/minimax-m3",
		"z-ai/glm-5.2",
		"deepseek-ai/deepseek-v4-pro",
		"deepseek-ai/deepseek-v4-flash",
		"deepseek-ai/deepseek-v4-flash-0731",
		"nvidia/deepseek-ai/deepseek-v4-flash",
		"nvidia/deepseek-ai/deepseek-v4-flash-0731",
	} {
		hint := descriptor.CapabilityHint(modelID)
		if hint.Kind != KindChat || !hint.SupportsReasoning || hint.ReasoningWireFormat != ReasoningWireOpenAI {
			t.Fatalf("hint for %s = %#v", modelID, hint)
		}
	}
}

func TestDescriptorWithBaseURLRewritesEveryEndpoint(t *testing.T) {
	base, err := url.Parse("http://127.0.0.1:12345")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	descriptor, err := DefaultDescriptor().WithBaseURL(base)
	if err != nil {
		t.Fatalf("WithBaseURL: %v", err)
	}
	wants := map[string]string{
		"models":    "http://127.0.0.1:12345/v1/models",
		"chat":      "http://127.0.0.1:12345/v1/chat/completions",
		"embedding": "http://127.0.0.1:12345/v1/embeddings",
		"asr":       "http://127.0.0.1:12345/v1/audio/transcriptions",
		"tts":       "http://127.0.0.1:12345/v1/audio/speech",
	}
	got := map[string]string{
		"models": descriptor.Models.URL, "chat": descriptor.Chat.URL,
		"embedding": descriptor.Embedding.URL, "asr": descriptor.ASR.URL, "tts": descriptor.TTS.URL,
	}
	for name, want := range wants {
		if got[name] != want {
			t.Fatalf("%s URL = %q, want %q", name, got[name], want)
		}
	}
}

func TestDescriptorValidateRejectsBrokenPublicKind(t *testing.T) {
	descriptor := DefaultDescriptor()
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("default Validate: %v", err)
	}
	descriptor.ASR = Endpoint{}
	if err := descriptor.Validate(); err == nil {
		t.Fatal("Validate accepted ASR capability without an endpoint")
	}
}

func TestDescriptorValidateRejectsInvalidSemantics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Descriptor)
	}{
		{"URL scheme", func(value *Descriptor) { value.Chat.URL = "http://integrate.api.nvidia.com/v1/chat/completions" }},
		{"models method", func(value *Descriptor) { value.Models.Method = http.MethodPost }},
		{"chat content type", func(value *Descriptor) { value.Chat.ContentType = "text/plain" }},
		{"auth scheme", func(value *Descriptor) { value.AuthScheme = "Basic" }},
		{"wire format", func(value *Descriptor) {
			value.CapabilityHints["bad/model"] = CapabilityHint{Kind: KindChat, SupportsReasoning: true, ReasoningWireFormat: "native"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := DefaultDescriptor()
			test.mutate(&descriptor)
			if err := descriptor.Validate(); err == nil {
				t.Fatal("Validate accepted invalid descriptor")
			}
		})
	}
}
