package audio

import (
	"encoding/json"
	"fmt"
	"strings"

	"nvidia-router/internal/modelcatalog"
)

const MaxSpeechRequestBytes = 32 << 20

// SpeechRequest validates routing fields while retaining the original JSON for
// upstream forwarding. Input text is deliberately not copied into a named field.
type SpeechRequest struct {
	fields      map[string]json.RawMessage
	publicModel string
}

func ParseSpeech(payload []byte) (SpeechRequest, error) {
	if len(payload) > MaxSpeechRequestBytes {
		return SpeechRequest{}, invalidRequest("request_too_large", "body", "The request body exceeds the 32 MiB limit.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return SpeechRequest{}, invalidRequest("invalid_json", "", "The request body must be a JSON object.")
	}
	modelID, err := requiredSpeechString(fields, "model")
	if err != nil {
		return SpeechRequest{}, err
	}
	if _, err := requiredSpeechString(fields, "input"); err != nil {
		return SpeechRequest{}, err
	}
	for _, name := range []string{"voice", "response_format"} {
		if err := optionalSpeechString(fields, name); err != nil {
			return SpeechRequest{}, err
		}
	}
	return SpeechRequest{fields: fields, publicModel: modelID}, nil
}

func (r SpeechRequest) PublicModelID() string {
	return r.publicModel
}

func (r SpeechRequest) Requirements() modelcatalog.Requirements {
	return modelcatalog.Requirements{Kind: modelcatalog.KindTTS}
}

func (r SpeechRequest) MarshalFor(model modelcatalog.Model) ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(r.fields))
	for name, raw := range r.fields {
		fields[name] = raw
	}
	mappedModel, err := json.Marshal(model.UpstreamID)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream speech model ID: %w", err)
	}
	fields["model"] = mappedModel
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal speech request: %w", err)
	}
	return body, nil
}

func requiredSpeechString(fields map[string]json.RawMessage, name string) (string, error) {
	raw, ok := fields[name]
	if !ok {
		return "", invalidRequest("missing_required_parameter", name, "The "+name+" parameter is required.")
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", invalidRequest("invalid_parameter", name, "The "+name+" parameter must be a non-empty string.")
	}
	return value, nil
}

func optionalSpeechString(fields map[string]json.RawMessage, name string) error {
	raw, ok := fields[name]
	if !ok {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return invalidRequest("invalid_parameter", name, "The "+name+" parameter must be a non-empty string.")
	}
	return nil
}
