package responses

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nvidia-router/internal/modelcatalog"
)

const maxResponsesBytes = 32 << 20

// ToChat converts a Responses API request body into an OpenAI Chat Completions
// request body targeting the resolved upstream model. It owns the full mapping:
// nothing from the Responses request is forwarded verbatim.
func ToChat(body []byte, model modelcatalog.Model) ([]byte, error) {
	if len(body) > maxResponsesBytes {
		return nil, invalidResponses("request_too_large", "body", "The request body exceeds the 32 MiB limit.")
	}
	fields, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	if err := rejectUnsupportedTopLevel(fields); err != nil {
		return nil, err
	}
	if err := rejectHostedTools(fields); err != nil {
		return nil, err
	}
	if model.Kind != modelcatalog.KindChat || !model.Enabled {
		return nil, invalidResponses("model_capability_unsupported", "model", "The selected model is not a chat model.")
	}
	modelID, err := requiredModel(fields)
	if err != nil {
		return nil, err
	}
	if modelID != model.PublicID {
		return nil, invalidResponses("invalid_parameter", "model", "The model parameter does not match the resolved model.")
	}
	messages, requirements, err := convertInput(fields)
	if err != nil {
		return nil, err
	}
	if instruction, ok := fields["instructions"]; ok {
		systemMessage, err := convertInstructions(instruction)
		if err != nil {
			return nil, err
		}
		if systemMessage != nil {
			messages = append([]chatMessage{*systemMessage}, messages...)
		}
	}
	if len(messages) == 0 {
		return nil, invalidResponses("missing_required_parameter", "input", "The input parameter is required.")
	}
	if requirements.vision && !model.SupportsVision {
		return nil, unsupportedResponses("input", "Vision inputs are not supported.")
	}

	chat := map[string]json.RawMessage{}
	encodedModel, _ := json.Marshal(model.UpstreamID)
	chat["model"] = encodedModel
	messagesBytes, _ := json.Marshal(messages)
	chat["messages"] = messagesBytes

	if err := mapTools(fields, chat); err != nil {
		return nil, err
	}
	if err := mapToolChoice(fields, chat); err != nil {
		return nil, err
	}
	if requirements.tools && !model.SupportsTools {
		return nil, unsupportedResponses("tools", "The selected model does not support tools.")
	}
	if err := mapReasoning(fields, model, chat); err != nil {
		return nil, err
	}
	if err := mapMaxOutputTokens(fields, chat); err != nil {
		return nil, err
	}
	if stream, ok := fields["stream"]; ok {
		chat["stream"] = stream
	} else {
		chat["stream"] = jsonRawFalse()
	}
	return marshalStable(chat)
}

type inputRequirements struct {
	vision bool
	tools  bool
}

// convertInstructions turns the Responses instructions string into a preceding
// system message, matching the Chat protocol's system role.
func convertInstructions(raw json.RawMessage) (*chatMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, invalidResponses("invalid_parameter", "instructions", "The instructions parameter must be a string.")
	}
	if value == "" {
		return nil, nil
	}
	return &chatMessage{Role: "system", RolePresent: true, Content: value, ContentPresent: true}, nil
}

func convertInput(fields map[string]json.RawMessage) ([]chatMessage, inputRequirements, error) {
	raw, ok := fields["input"]
	if !ok {
		return nil, inputRequirements{}, invalidResponses("missing_required_parameter", "input", "The input parameter is required.")
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil && asString != "" {
		return []chatMessage{{Role: "user", RolePresent: true, Content: asString, ContentPresent: true}}, inputRequirements{}, nil
	}

	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, inputRequirements{}, invalidResponses("invalid_parameter", "input", "The input parameter must be a string or an array.")
	}

	messages := make([]chatMessage, 0, len(items))
	requirements := inputRequirements{}
	for index, item := range items {
		message, wantsTools, wantsVision, err := convertInputItem(item, index)
		if err != nil {
			return nil, inputRequirements{}, err
		}
		messages = append(messages, message)
		requirements.tools = requirements.tools || wantsTools
		requirements.vision = requirements.vision || wantsVision
	}
	if len(messages) == 0 {
		return nil, inputRequirements{}, invalidResponses("invalid_parameter", "input", "The input array must not be empty.")
	}
	return messages, requirements, nil
}

func convertInputItem(item json.RawMessage, index int) (chatMessage, bool, bool, error) {
	var parsed responsesInputItem
	if err := json.Unmarshal(item, &parsed); err != nil {
		return chatMessage{}, false, false, invalidResponses("invalid_parameter", fmt.Sprintf("input[%d]", index), "Each input entry must be an object.")
	}
	switch parsed.Type {
	case "":
		return convertMessageItem(parsed, index)
	case "message":
		return convertMessageItem(parsed, index)
	case "function_call":
		return convertFunctionCallItem(parsed, index)
	case "function_call_output":
		return convertFunctionCallOutputItem(parsed, index)
	case "file":
		return chatMessage{}, false, false, unsupportedResponses(fmt.Sprintf("input[%d]", index), "File inputs are not supported.")
	default:
		return chatMessage{}, false, false, unsupportedResponses(fmt.Sprintf("input[%d]", index), "This input type is not supported.")
	}
}

func convertMessageItem(parsed responsesInputItem, index int) (chatMessage, bool, bool, error) {
	param := fmt.Sprintf("input[%d]", index)
	if parsed.Role == "" {
		parsed.Role = "user"
	}
	switch parsed.Role {
	case "system", "user", "assistant":
	default:
		return chatMessage{}, false, false, invalidResponses("invalid_parameter", param+".role", "The message role is not supported.")
	}
	switch parsed.Role {
	case "system", "user":
		text, usesVision, isString, err := extractMessageText(parsed, param)
		if err != nil {
			return chatMessage{}, false, false, err
		}
		if isString && text == "" {
			return chatMessage{}, false, false, invalidResponses("invalid_parameter", param+".content", "The message content must be non-empty.")
		}
		return chatMessage{Role: parsed.Role, RolePresent: true, Content: text, ContentPresent: true}, false, usesVision, nil
	default: // assistant
		text, _, isString, err := extractMessageText(parsed, param)
		if err != nil {
			return chatMessage{}, false, false, err
		}
		message := chatMessage{Role: "assistant", RolePresent: true}
		if text != "" || isString {
			message.Content, message.ContentPresent = text, true
		}
		return message, false, false, nil
	}
}

func extractMessageText(parsed responsesInputItem, param string) (text string, usesVision, isString bool, err error) {
	if len(parsed.Content) == 0 {
		return "", false, true, nil
	}
	var raw string
	if json.Unmarshal(parsed.Content, &raw) == nil {
		return raw, false, true, nil
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(parsed.Content, &parts) != nil {
		return "", false, false, invalidResponses("invalid_parameter", param+".content", "The message content must be text or an array of text parts.")
	}
	for partIndex, part := range parts {
		var kind string
		if rawType, ok := part["type"]; ok {
			_ = json.Unmarshal(rawType, &kind)
		}
		switch kind {
		case "input_text", "text", "output_text", "":
			if rawText, ok := part["text"]; ok {
				var textValue string
				_ = json.Unmarshal(rawText, &textValue)
				text += textValue
			}
		case "input_image", "image", "image_url", "output_image":
			return "", true, false, unsupportedResponses(fmt.Sprintf("%s.content[%d]", param, partIndex), "Image inputs are not supported.")
		case "file":
			return "", true, false, unsupportedResponses(fmt.Sprintf("%s.content[%d]", param, partIndex), "File inputs are not supported.")
		default:
			return "", true, false, unsupportedResponses(fmt.Sprintf("%s.content[%d]", param, partIndex), "This content type is not supported.")
		}
	}
	return text, false, false, nil
}

func convertFunctionCallItem(parsed responsesInputItem, index int) (chatMessage, bool, bool, error) {
	param := fmt.Sprintf("input[%d]", index)
	if parsed.CallID == "" || parsed.Name == "" {
		return chatMessage{}, false, false, invalidResponses("invalid_parameter", param, "Function call entries require a name and call_id.")
	}
	if parsed.Arguments == "" {
		parsed.Arguments = "{}"
	}
	call := chatToolCall{Index: 0, ID: parsed.CallID, Type: "function", Function: chatFunction{Name: parsed.Name, Arguments: parsed.Arguments}}
	return chatMessage{Role: "assistant", RolePresent: true, ToolCalls: []chatToolCall{call}}, true, false, nil
}

func convertFunctionCallOutputItem(parsed responsesInputItem, index int) (chatMessage, bool, bool, error) {
	param := fmt.Sprintf("input[%d]", index)
	if parsed.CallID == "" {
		return chatMessage{}, false, false, invalidResponses("missing_required_parameter", param+".call_id", "Function call output entries require a call_id.")
	}
	output := normaliseFunctionOutput(parsed.Output)
	return chatMessage{Role: "tool", RolePresent: true, ToolCallID: parsed.CallID, ToolCallIDPresent: true, Content: output, ContentPresent: true}, true, false, nil
}

func normaliseFunctionOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString
	}
	return strings.TrimSpace(string(raw))
}

// mapTools forwards the function tool array re-encoded as Chat function tools.
// Only function-type tools reach this stage; hosting was rejected earlier.
func mapTools(fields map[string]json.RawMessage, chat map[string]json.RawMessage) error {
	raw, ok := fields["tools"]
	if !ok {
		return nil
	}
	var tools []struct {
		Type     json.RawMessage `json:"type"`
		Function json.RawMessage `json:"function"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return invalidResponses("invalid_parameter", "tools", "The tools parameter must be an array.")
	}
	normalised := make([]map[string]json.RawMessage, 0, len(tools))
	for index, tool := range tools {
		var toolType string
		if json.Unmarshal(tool.Type, &toolType) != nil || toolType != "function" {
			if toolType == "" {
				toolType = "function"
			} else {
				return unsupportedResponses(fmt.Sprintf("tools[%d].type", index), "Only function tools are supported.")
			}
		}
		entry := map[string]json.RawMessage{"type": jsonRawString("function")}
		if len(tool.Function) > 0 {
			entry["function"] = tool.Function
		} else {
			return invalidResponses("missing_required_parameter", fmt.Sprintf("tools[%d].function", index), "A function tool requires a function definition.")
		}
		normalised = append(normalised, entry)
	}
	bytes, err := json.Marshal(normalised)
	if err != nil {
		return fmt.Errorf("marshal tools: %w", err)
	}
	chat["tools"] = bytes
	return nil
}

func mapToolChoice(fields map[string]json.RawMessage, chat map[string]json.RawMessage) error {
	raw, ok := fields["tool_choice"]
	if !ok {
		return nil
	}
	chat["tool_choice"] = raw
	return nil
}

func mapReasoning(fields map[string]json.RawMessage, model modelcatalog.Model, chat map[string]json.RawMessage) error {
	raw, ok := fields["reasoning"]
	if !ok {
		// Allow passthrough of a native chat reasoning_effort request field too.
		if native, ok := fields["reasoning_effort"]; ok {
			chat["reasoning_effort"] = native
			return nil
		}
		return nil
	}
	var reasoning struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(raw, &reasoning); err != nil || reasoning.Effort == "" {
		return invalidResponses("invalid_parameter", "reasoning", "The reasoning parameter must include a supported effort.")
	}
	if !model.SupportsReasoning || model.ReasoningWireFormat != "openai" {
		return unsupportedResponses("reasoning", "The selected model does not support reasoning.")
	}
	chat["reasoning_effort"] = jsonRawString(reasoning.Effort)
	return nil
}

func mapMaxOutputTokens(fields map[string]json.RawMessage, chat map[string]json.RawMessage) error {
	raw, ok := fields["max_output_tokens"]
	if !ok {
		return nil
	}
	var value *int
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return invalidResponses("invalid_parameter", "max_output_tokens", "The max_output_tokens parameter must be an integer.")
	}
	if *value <= 0 {
		return invalidResponses("invalid_parameter", "max_output_tokens", "The max_output_tokens parameter must be positive.")
	}
	encoded, _ := json.Marshal(*value)
	chat["max_tokens"] = encoded
	return nil
}

func requiredModel(fields map[string]json.RawMessage) (string, error) {
	raw, ok := fields["model"]
	if !ok {
		return "", invalidResponses("missing_required_parameter", "model", "The model parameter is required.")
	}
	var modelID string
	if json.Unmarshal(raw, &modelID) != nil || strings.TrimSpace(modelID) != modelID || modelID == "" {
		return "", invalidResponses("invalid_parameter", "model", "The model parameter must be a non-empty string.")
	}
	return modelID, nil
}

func decodeObject(body []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, invalidResponses("invalid_json", "", "The request body must be a JSON object.")
	}
	return fields, nil
}

func marshalStable(fields map[string]json.RawMessage) ([]byte, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	builder.WriteByte('{')
	for index, name := range names {
		if index > 0 {
			builder.WriteByte(',')
		}
		encodedName, _ := json.Marshal(name)
		builder.Write(encodedName)
		builder.WriteByte(':')
		builder.Write(fields[name])
	}
	builder.WriteByte('}')
	if !json.Valid([]byte(builder.String())) {
		return nil, fmt.Errorf("marshal chat request: invalid raw JSON")
	}
	return []byte(builder.String()), nil
}

func jsonRawString(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func jsonRawFalse() json.RawMessage {
	return json.RawMessage("false")
}
