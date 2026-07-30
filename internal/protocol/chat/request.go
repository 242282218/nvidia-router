package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nvidia-router/internal/modelcatalog"
)

const MaxRequestBytes = 32 << 20

type Request struct {
	fields       map[string]json.RawMessage
	publicModel  string
	stream       bool
	requirements modelcatalog.Requirements
}

func Parse(payload []byte) (Request, error) {
	if len(payload) > MaxRequestBytes {
		return Request{}, invalidRequest("request_too_large", "body", "The request body exceeds the 32 MiB limit.")
	}
	fields, err := decodeFields(payload)
	if err != nil {
		return Request{}, err
	}
	if err := rejectUnsupported(fields); err != nil {
		return Request{}, err
	}
	modelID, err := requiredModel(fields)
	if err != nil {
		return Request{}, err
	}
	vision, messageTools, err := validateMessages(fields)
	if err != nil {
		return Request{}, err
	}
	stream, err := optionalStream(fields)
	if err != nil {
		return Request{}, err
	}
	tools, err := validateTools(fields)
	if err != nil {
		return Request{}, err
	}
	toolChoice, err := validateToolChoice(fields)
	if err != nil {
		return Request{}, err
	}
	return Request{
		fields: fields, publicModel: modelID, stream: stream,
		requirements: modelcatalog.Requirements{
			Kind: modelcatalog.KindChat, Vision: vision, Tools: messageTools || tools || toolChoice, Reasoning: hasReasoning(fields),
		},
	}, nil
}

func (r Request) PublicModelID() string {
	return r.publicModel
}

func (r Request) Stream() bool {
	return r.stream
}

func (r Request) Requirements() modelcatalog.Requirements {
	return r.requirements
}

func (r Request) MarshalFor(model modelcatalog.Model) ([]byte, error) {
	if err := validateModel(r, model); err != nil {
		return nil, err
	}
	fields := cloneFields(r.fields)
	if err := normalizeReasoning(fields); err != nil {
		return nil, err
	}
	mappedModel, err := json.Marshal(model.UpstreamID)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream model ID: %w", err)
	}
	fields["model"] = mappedModel
	return marshalFields(fields)
}

func decodeFields(payload []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return nil, invalidRequest("invalid_json", "", "The request body must be a JSON object.")
	}
	return fields, nil
}

func requiredModel(fields map[string]json.RawMessage) (string, error) {
	raw, ok := fields["model"]
	if !ok {
		return "", invalidRequest("missing_required_parameter", "model", "The model parameter is required.")
	}
	var modelID string
	if json.Unmarshal(raw, &modelID) != nil || modelID == "" || strings.TrimSpace(modelID) != modelID {
		return "", invalidRequest("invalid_parameter", "model", "The model parameter must be a non-empty string.")
	}
	return modelID, nil
}

func cloneFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(fields))
	for name, raw := range fields {
		cloned[name] = raw
	}
	return cloned
}

func marshalFields(fields map[string]json.RawMessage) ([]byte, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var output bytes.Buffer
	output.WriteByte('{')
	for index, name := range names {
		if index > 0 {
			output.WriteByte(',')
		}
		encodedName, _ := json.Marshal(name)
		output.Write(encodedName)
		output.WriteByte(':')
		output.Write(fields[name])
	}
	output.WriteByte('}')
	if !json.Valid(output.Bytes()) {
		return nil, fmt.Errorf("marshal chat request: invalid raw JSON")
	}
	return output.Bytes(), nil
}
