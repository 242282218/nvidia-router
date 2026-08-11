package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
)

const MaxRequestBytes = 32 << 20

// Request captures and validates an OpenAI Embeddings request. Unknown fields
// are preserved verbatim so the upstream request can carry them unchanged;
// input text is never copied into logs or stored fields.
type Request struct {
	fields       map[string]json.RawMessage
	publicModel  string
	inputs       []string
	requirements modelcatalog.Requirements
}

// Parse validates a raw Embeddings payload. The input field accepts either a
// single string or an array of strings; an empty array, empty string, or an
// absent input is rejected before the request touches the upstream.
func Parse(payload []byte) (Request, error) {
	if len(payload) > MaxRequestBytes {
		return Request{}, invalidRequest("request_too_large", "body", "The request body exceeds the 32 MiB limit.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return Request{}, invalidRequest("invalid_json", "", "The request body must be a JSON object.")
	}
	modelID, err := requiredModel(fields)
	if err != nil {
		return Request{}, err
	}
	inputs, err := requiredInput(fields)
	if err != nil {
		return Request{}, err
	}
	return Request{
		fields: fields, publicModel: modelID, inputs: inputs,
		requirements: modelcatalog.Requirements{Kind: modelcatalog.KindEmbedding},
	}, nil
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

func requiredInput(fields map[string]json.RawMessage) ([]string, error) {
	raw, ok := fields["input"]
	if !ok {
		return nil, invalidRequest("missing_required_parameter", "input", "The input parameter is required.")
	}
	switch inferInputKind(raw) {
	case inputKindString:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
			return nil, invalidRequest("invalid_parameter", "input", "The input must be a non-empty string.")
		}
		return []string{value}, nil
	case inputKindArray:
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, invalidRequest("invalid_parameter", "input", "The input array must contain only strings.")
		}
		if len(values) == 0 {
			return nil, invalidRequest("invalid_parameter", "input", "The input array must not be empty.")
		}
		for index, value := range values {
			if strings.TrimSpace(value) == "" {
				return nil, invalidRequest("invalid_parameter", "input",
					fmt.Sprintf("input[%d] must be a non-empty string.", index))
			}
		}
		return values, nil
	default:
		return nil, invalidRequest("invalid_parameter", "input", "The input must be a string or an array of strings.")
	}
}

// PublicModelID returns the public model identifier so the resolver can locate
// the whitelist entry.
func (r Request) PublicModelID() string {
	return r.publicModel
}

// Requirements declares that embeddings require a kind=embedding model.
func (r Request) Requirements() modelcatalog.Requirements {
	return r.requirements
}

// Inputs returns the parsed input texts. They are exposed to drive the
// exact-match cache's cache key; callers must hash them, never log them.
func (r Request) Inputs() []string {
	return r.inputs
}

// MarshalFor rebuilds the upstream body after model mapping. Unknown fields are
// preserved; only the model field is rewritten to the upstream ID. Input text is
// forwarded inline and never stored on the Request.
func (r Request) MarshalFor(model modelcatalog.Model) ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(r.fields))
	for name, raw := range r.fields {
		fields[name] = raw
	}
	mappedModel, err := json.Marshal(model.UpstreamID)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream model ID: %w", err)
	}
	fields["model"] = mappedModel
	return marshalFields(fields)
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
		return nil, fmt.Errorf("marshal embeddings request: invalid raw JSON")
	}
	return output.Bytes(), nil
}

type inputKind int

const (
	inputKindUnknown inputKind = iota
	inputKindString
	inputKindArray
)

// inferInputKind inspects the first non-space byte of a JSON value to decide
// whether the input is a string or an array, so the validator can reject the
// wrong shape before unmarshalling.
func inferInputKind(raw json.RawMessage) inputKind {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			return inputKindString
		case '[':
			return inputKindArray
		default:
			return inputKindUnknown
		}
	}
	return inputKindUnknown
}

func invalidRequest(code, param, message string) *apierror.Error {
	var parameter *string
	if param != "" {
		parameter = &param
	}
	return &apierror.Error{
		Status: 400, Type: "invalid_request_error", Code: code, Message: message, Param: parameter,
	}
}
