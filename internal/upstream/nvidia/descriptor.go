package nvidia

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type ModelKind string

const (
	KindChat      ModelKind = "chat"
	KindEmbedding ModelKind = "embedding"
	KindASR       ModelKind = "asr"
	KindTTS       ModelKind = "tts"
)

type ReasoningWireFormat string

const (
	ReasoningWireNone   ReasoningWireFormat = "none"
	ReasoningWireOpenAI ReasoningWireFormat = "openai"
)

type Endpoint struct {
	Method      string
	URL         string
	ContentType string
}

type CapabilityHint struct {
	Kind                ModelKind
	SupportsVision      bool
	SupportsTools       bool
	SupportsReasoning   bool
	ReasoningWireFormat ReasoningWireFormat
}

type Descriptor struct {
	AuthScheme      string
	Models          Endpoint
	Chat            Endpoint
	Embedding       Endpoint
	ASR             Endpoint
	TTS             Endpoint
	CapabilityHints map[string]CapabilityHint
}

func DefaultDescriptor() Descriptor {
	const baseURL = "https://integrate.api.nvidia.com/v1"
	return Descriptor{
		AuthScheme:      "Bearer",
		Models:          Endpoint{Method: http.MethodGet, URL: baseURL + "/models"},
		Chat:            Endpoint{Method: http.MethodPost, URL: baseURL + "/chat/completions", ContentType: "application/json"},
		Embedding:       Endpoint{Method: http.MethodPost, URL: baseURL + "/embeddings", ContentType: "application/json"},
		ASR:             Endpoint{Method: http.MethodPost, URL: baseURL + "/audio/transcriptions", ContentType: "multipart/form-data"},
		TTS:             Endpoint{Method: http.MethodPost, URL: baseURL + "/audio/speech", ContentType: "application/json"},
		CapabilityHints: reasoningHints(),
	}
}

func (d Descriptor) WithBaseURL(base *url.URL) (Descriptor, error) {
	if base == nil || base.Host == "" || base.Scheme != "https" && base.Scheme != "http" {
		return Descriptor{}, errors.New("rewrite NVIDIA endpoints: base URL must use HTTP or HTTPS and include a host")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return Descriptor{}, errors.New("rewrite NVIDIA endpoints: base URL must not contain credentials, query, or fragment")
	}
	endpointURL := func(path string) string {
		value := *base
		value.Path = strings.TrimRight(value.Path, "/") + path
		value.RawPath = ""
		return value.String()
	}
	d.Models.URL = endpointURL("/v1/models")
	d.Chat.URL = endpointURL("/v1/chat/completions")
	d.Embedding.URL = endpointURL("/v1/embeddings")
	d.ASR.URL = endpointURL("/v1/audio/transcriptions")
	d.TTS.URL = endpointURL("/v1/audio/speech")
	return d, nil
}

func reasoningHints() map[string]CapabilityHint {
	models := []string{
		"minimaxai/minimax-m2.7",
		"minimaxai/minimax-m3",
		"z-ai/glm-5.2",
		"deepseek-ai/deepseek-v4-pro",
		"deepseek-ai/deepseek-v4-flash",
	}
	hints := make(map[string]CapabilityHint, len(models))
	for _, model := range models {
		hints[model] = CapabilityHint{
			Kind:                KindChat,
			SupportsReasoning:   true,
			ReasoningWireFormat: ReasoningWireOpenAI,
		}
	}
	return hints
}

func (d Descriptor) CapabilityHint(modelID string) CapabilityHint {
	if hint, ok := d.CapabilityHints[modelID]; ok {
		return hint
	}
	return CapabilityHint{Kind: KindChat, ReasoningWireFormat: ReasoningWireNone}
}

func (d Descriptor) NewRequest(endpoint Endpoint, stream bool, token string) (*http.Request, error) {
	request, err := http.NewRequest(endpoint.Method, endpoint.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create NVIDIA request: %w", err)
	}
	request.Header.Set("Authorization", d.AuthScheme+" "+token)
	if endpoint.ContentType != "" {
		request.Header.Set("Content-Type", endpoint.ContentType)
	}
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	return request, nil
}

func (d Descriptor) Validate() error {
	if d.AuthScheme != "Bearer" {
		return fmt.Errorf("validate NVIDIA auth scheme: %w", errors.New("bearer is required"))
	}
	endpoints := []struct {
		name        string
		endpoint    Endpoint
		method      string
		contentType string
	}{
		{"models", d.Models, http.MethodGet, ""},
		{"chat", d.Chat, http.MethodPost, "application/json"},
		{"embedding", d.Embedding, http.MethodPost, "application/json"},
		{"asr", d.ASR, http.MethodPost, "multipart/form-data"},
		{"tts", d.TTS, http.MethodPost, "application/json"},
	}
	for _, item := range endpoints {
		if err := validateEndpoint(item.endpoint, item.method, item.contentType); err != nil {
			return fmt.Errorf("validate NVIDIA %s endpoint: %w", item.name, err)
		}
	}
	for modelID, hint := range d.CapabilityHints {
		if err := validateHint(hint); err != nil {
			return fmt.Errorf("validate NVIDIA capability hint %q: %w", modelID, err)
		}
	}
	return nil
}

func validateEndpoint(endpoint Endpoint, method, contentType string) error {
	if endpoint.Method != method {
		return fmt.Errorf("method must be %s", method)
	}
	if endpoint.ContentType != contentType {
		return fmt.Errorf("Content-Type must be %q", contentType)
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("URL must use HTTPS and include a host")
	}
	return nil
}

func validateHint(hint CapabilityHint) error {
	switch hint.Kind {
	case KindChat, KindEmbedding, KindASR, KindTTS:
	default:
		return fmt.Errorf("unsupported model kind %q", hint.Kind)
	}
	switch hint.ReasoningWireFormat {
	case ReasoningWireNone:
		if hint.SupportsReasoning {
			return errors.New("reasoning models require a wire format")
		}
	case ReasoningWireOpenAI:
		if !hint.SupportsReasoning {
			return errors.New("OpenAI reasoning wire format requires reasoning support")
		}
	default:
		return fmt.Errorf("unsupported reasoning wire format %q", hint.ReasoningWireFormat)
	}
	return nil
}
