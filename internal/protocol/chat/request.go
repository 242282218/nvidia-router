package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"nvidia-router/internal/compat"
	"nvidia-router/internal/modelcatalog"
)

const MaxRequestBytes = 32 << 20

type Request struct {
	fields        map[string]json.RawMessage
	publicModel   string
	stream        bool
	requirements  modelcatalog.Requirements
	tools         []compat.ToolDefinition
	toolChoice    compat.ToolChoice
	toolChoiceSet bool
	reasoning     compat.ReasoningSpec
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
	if needsChatMessageNormalization(fields["messages"]) {
		normalizedMessages, err := normalizeChatMessages(fields["messages"])
		if err != nil {
			return Request{}, err
		}
		fields["messages"] = normalizedMessages
	}
	stream, err := optionalStream(fields)
	if err != nil {
		return Request{}, err
	}
	tools, err := validateTools(fields)
	if err != nil {
		return Request{}, err
	}
	_, err = validateToolChoice(fields)
	if err != nil {
		return Request{}, err
	}
	normalizedTools, err := compat.NormalizeTools(fields["tools"], compat.ToolFormatChat, "tools")
	if err != nil {
		return Request{}, compatRequestError(err)
	}
	normalizedChoice, err := compat.NormalizeToolChoice(fields["tool_choice"], compat.ToolNames(normalizedTools), "tool_choice")
	if err != nil {
		return Request{}, compatRequestError(err)
	}
	reasoning, err := compat.ParseReasoning(fields)
	if err != nil {
		if errors.Is(err, compat.ErrAmbiguousReasoning) {
			return Request{}, invalidRequest("invalid_parameter", "reasoning", "Reasoning aliases must describe the same level and budget.")
		}
		return Request{}, compatRequestError(err)
	}
	return Request{
		fields: fields, publicModel: modelID, stream: stream,
		requirements: modelcatalog.Requirements{
			Kind: modelcatalog.KindChat, Vision: vision, Tools: messageTools || tools || len(normalizedTools) > 0 || normalizedChoice.Mode == "function" || normalizedChoice.Mode == "required", Reasoning: reasoning.Requested,
		},
		tools: normalizedTools, toolChoice: normalizedChoice,
		toolChoiceSet: fields["tool_choice"] != nil, reasoning: reasoning,
	}, nil
}

func needsChatMessageNormalization(raw json.RawMessage) bool {
	for _, key := range []string{`"tool_calls"`, `"function_call"`, `"tool_call_id"`, `"call_id"`, `"function"`} {
		if bytes.Contains(raw, []byte(key)) {
			return true
		}
	}
	return false
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
	mappedModel, err := json.Marshal(model.UpstreamID)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream model ID: %w", err)
	}
	fields["model"] = mappedModel
	if len(r.tools) > 0 {
		encodedTools, err := compat.MarshalTools(r.tools, compat.ToolFormatChat)
		if err != nil {
			return nil, fmt.Errorf("marshal normalized chat tools: %w", err)
		}
		fields["tools"] = encodedTools
	}
	if r.toolChoiceSet {
		encodedChoice, err := compat.MarshalToolChoice(r.toolChoice)
		if err != nil {
			return nil, fmt.Errorf("marshal normalized chat tool choice: %w", err)
		}
		fields["tool_choice"] = encodedChoice
	}
	if r.reasoning.Requested && model.SupportsReasoning {
		decision, err := compat.ResolveReasoning(r.reasoning, model.ReasoningProfile())
		if err != nil {
			return nil, reasoningModelError(err)
		}
		if err := compat.ApplyReasoning(fields, decision, model.ReasoningProfile()); err != nil {
			return nil, reasoningModelError(err)
		}
	}
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
