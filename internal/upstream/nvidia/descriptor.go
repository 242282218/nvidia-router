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
	ReasoningWireNone     ReasoningWireFormat = "none"
	ReasoningWireOpenAI   ReasoningWireFormat = "openai"
	ReasoningWireThinking ReasoningWireFormat = "thinking"
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

// OpenAICompatibleDescriptor builds a descriptor for an arbitrary
// OpenAI-compatible base URL (e.g. SiliconFlow's https://api.siliconflow.cn/v1).
// It deliberately carries no vendor-specific capability hints: those are
// verified through the model catalog, and guessing them for an unknown upstream
// would be worse than the neutral default. Only the chat/embeddings/models
// surface is offered — ASR/TTS are NVIDIA-specific transports and stay absent.
func OpenAICompatibleDescriptor(baseURL string) (Descriptor, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse OpenAI-compatible base URL: %w", err)
	}
	if parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return Descriptor{}, errors.New("OpenAI-compatible base URL must use HTTP(S) and include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Descriptor{}, errors.New("OpenAI-compatible base URL must not contain credentials, query, or fragment")
	}
	endpointURL := func(path string) string {
		value := *parsed
		value.Path = strings.TrimRight(value.Path, "/") + path
		value.RawPath = ""
		return value.String()
	}
	return Descriptor{
		AuthScheme: "Bearer",
		Models:     Endpoint{Method: http.MethodGet, URL: endpointURL("/models")},
		Chat:       Endpoint{Method: http.MethodPost, URL: endpointURL("/chat/completions"), ContentType: "application/json"},
		Embedding:  Endpoint{Method: http.MethodPost, URL: endpointURL("/embeddings"), ContentType: "application/json"},
		// The OpenAI-compatible family does not expose NVIDIA's audio endpoints;
		// leaving them empty makes Validate reject the descriptor, so construction
		// only succeeds for providers that truly speak the common subset.
	}, nil
}

func reasoningHints() map[string]CapabilityHint {
	models := []string{
		"minimaxai/minimax-m2.7",
		"minimaxai/minimax-m3",
		"z-ai/glm-5.2",
		"deepseek-ai/deepseek-v4-pro",
		"deepseek-ai/deepseek-v4-flash",
		// NVIDIA versioned the flash model ID with a date suffix (the bare
		// deepseek-v4-flash now answers 410 Gone); keep both so discovery does
		// not downgrade reasoning for whichever ID is live.
		"deepseek-ai/deepseek-v4-flash-0731",
		"deepseek-ai/deepseek-r1",
		"deepseek-ai/deepseek-r1-distill-llama-70b",
		"deepseek-ai/deepseek-r1-distill-qwen-32b",
		"deepseek-ai/deepseek-r1-distill-qwen-14b",
		"deepseek-ai/deepseek-r1-distill-qwen-7b",
		"deepseek-ai/deepseek-r1-distill-qwen-1.5b",
		"deepseek-ai/deepseek-r1-distill-llama-8b",
		"deepseek-ai/deepseek-reasoner",
		"qwen/qwq-32b-preview",
		"qwen/qwq-32b",
		"openai/o1",
		"openai/o3-mini",
		"openai/o4-mini",
	}
	hints := make(map[string]CapabilityHint, len(models)+2)
	for _, model := range models {
		hints[model] = CapabilityHint{
			Kind:                KindChat,
			SupportsReasoning:   true,
			ReasoningWireFormat: ReasoningWireOpenAI,
		}
	}
	for _, model := range []string{
		"nvidia/nemotron-3-ultra-550b-a55b",
		"stepfun-ai/step-3.7-flash",
	} {
		hints[model] = CapabilityHint{
			Kind:                KindChat,
			SupportsReasoning:   true,
			ReasoningWireFormat: ReasoningWireThinking,
		}
	}
	return hints
}

func (d Descriptor) CapabilityHint(modelID string) CapabilityHint {
	for _, candidate := range capabilityModelIDs(modelID) {
		if hint, ok := d.CapabilityHints[candidate]; ok {
			return hint
		}
	}
	return CapabilityHint{Kind: KindChat, ReasoningWireFormat: ReasoningWireNone}
}

func capabilityModelIDs(modelID string) []string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	candidates := []string{modelID}
	const prefix = "nvidia/"
	if strings.HasPrefix(strings.ToLower(modelID), prefix) {
		candidates = append(candidates, modelID[len(prefix):])
	}
	return candidates
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
	case ReasoningWireOpenAI, ReasoningWireThinking:
		if !hint.SupportsReasoning {
			return errors.New("reasoning wire format requires reasoning support")
		}
	default:
		return fmt.Errorf("unsupported reasoning wire format %q", hint.ReasoningWireFormat)
	}
	return nil
}
