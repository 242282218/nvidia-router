package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ToolFormat string

const (
	ToolFormatChat      ToolFormat = "chat"
	ToolFormatResponses ToolFormat = "responses"
)

type ValidationError struct {
	Code    string
	Param   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "compatibility validation failed"
	}
	return e.Message
}

func invalid(code, param, message string) error {
	return &ValidationError{Code: code, Param: param, Message: message}
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Strict      *bool
}

type ToolChoice struct {
	Mode string
	Name string
}

func NormalizeTools(raw json.RawMessage, format ToolFormat, param string) ([]ToolDefinition, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, invalid("invalid_parameter", param, "The tools parameter must be an array.")
	}
	definitions := make([]ToolDefinition, 0, len(items))
	for index, item := range items {
		definition, err := normalizeTool(item, format, fmt.Sprintf("%s[%d]", param, index))
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func normalizeTool(item map[string]json.RawMessage, format ToolFormat, param string) (ToolDefinition, error) {
	if item == nil {
		return ToolDefinition{}, invalid("invalid_parameter", param, "Each tool must be an object.")
	}
	toolType := ""
	if rawType, ok := item["type"]; ok {
		if err := json.Unmarshal(rawType, &toolType); err != nil {
			return ToolDefinition{}, invalid("invalid_parameter", param+".type", "The tool type must be a string.")
		}
	} else if _, nested := item["function"]; nested || item["name"] != nil {
		toolType = "function"
	}
	if toolType != "function" {
		return ToolDefinition{}, invalid("invalid_parameter", param+".type", "Only function tools are supported.")
	}

	definition := make(map[string]json.RawMessage)
	if rawFunction, nested := item["function"]; nested {
		if format == ToolFormatResponses && hasFlatDefinitionFields(item) {
			return ToolDefinition{}, invalid("invalid_parameter", param, "A function tool cannot mix flat and nested function fields.")
		}
		if err := json.Unmarshal(rawFunction, &definition); err != nil || definition == nil {
			return ToolDefinition{}, invalid("invalid_parameter", param+".function", "The function definition must be an object.")
		}
	} else {
		for key, value := range item {
			if key != "type" {
				definition[key] = value
			}
		}
	}
	if format == ToolFormatChat && len(definition) == 0 {
		return ToolDefinition{}, invalid("missing_required_parameter", param+".function.name", "A function tool name is required.")
	}
	if err := rejectUnknownDefinitionFields(definition, param, format); err != nil {
		return ToolDefinition{}, err
	}
	var result ToolDefinition
	if rawName, ok := definition["name"]; ok {
		if err := json.Unmarshal(rawName, &result.Name); err != nil || strings.TrimSpace(result.Name) != result.Name || result.Name == "" {
			return ToolDefinition{}, invalid("invalid_parameter", param+".function.name", "A function tool name must be a non-empty string.")
		}
	} else {
		return ToolDefinition{}, invalid("missing_required_parameter", param+".function.name", "A function tool name is required.")
	}
	if rawDescription, ok := definition["description"]; ok && !isNull(rawDescription) {
		if err := json.Unmarshal(rawDescription, &result.Description); err != nil {
			return ToolDefinition{}, invalid("invalid_parameter", param+".function.description", "The function description must be a string.")
		}
	}
	if rawParameters, ok := definition["parameters"]; ok && !isNull(rawParameters) {
		if !json.Valid(rawParameters) {
			return ToolDefinition{}, invalid("invalid_parameter", param+".function.parameters", "The function parameters must be valid JSON.")
		}
		result.Parameters = cloneBytes(rawParameters)
	}
	if rawStrict, ok := definition["strict"]; ok && !isNull(rawStrict) {
		var strict bool
		if json.Unmarshal(rawStrict, &strict) != nil {
			return ToolDefinition{}, invalid("invalid_parameter", param+".function.strict", "The function strict flag must be a boolean.")
		}
		result.Strict = &strict
	}
	return result, nil
}

func hasFlatDefinitionFields(item map[string]json.RawMessage) bool {
	for _, name := range []string{"name", "description", "parameters", "strict"} {
		if _, ok := item[name]; ok {
			return true
		}
	}
	return false
}

func rejectUnknownDefinitionFields(definition map[string]json.RawMessage, param string, format ToolFormat) error {
	for name, raw := range definition {
		switch name {
		case "name", "description", "parameters", "strict":
		case "allowed_callers", "defer_loading", "output_schema":
			if format == ToolFormatResponses {
				if !isNull(raw) {
					return invalid("unsupported_responses_feature", param+"."+name, unsupportedToolExtensionMessage(name))
				}
				continue
			}
			return invalid("invalid_parameter", param+".function."+name, "Unknown function tool field.")
		default:
			return invalid("invalid_parameter", param+".function."+name, "Unknown function tool field.")
		}
	}
	return nil
}

func unsupportedToolExtensionMessage(name string) string {
	switch name {
	case "allowed_callers":
		return "Allowed callers are not supported."
	case "defer_loading":
		return "Deferred tool loading is not supported."
	default:
		return "Output schemas are not supported."
	}
}

func MarshalTools(definitions []ToolDefinition, format ToolFormat) ([]byte, error) {
	items := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		function := map[string]any{"name": definition.Name}
		if definition.Description != "" {
			function["description"] = definition.Description
		}
		if len(definition.Parameters) > 0 {
			function["parameters"] = json.RawMessage(definition.Parameters)
		}
		if definition.Strict != nil {
			function["strict"] = *definition.Strict
		}
		if format == ToolFormatResponses {
			entry := map[string]any{"type": "function"}
			for name, value := range function {
				entry[name] = value
			}
			items = append(items, entry)
			continue
		}
		items = append(items, map[string]any{"type": "function", "function": function})
	}
	return json.Marshal(items)
}

func NormalizeToolChoice(raw json.RawMessage, names map[string]struct{}, param string) (ToolChoice, error) {
	if len(raw) == 0 || isNull(raw) {
		return ToolChoice{}, nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		switch mode {
		case "none", "auto", "required":
			if mode == "required" && len(names) == 0 {
				return ToolChoice{}, invalid("invalid_parameter", param, "required tool_choice needs at least one function tool.")
			}
			return ToolChoice{Mode: mode}, nil
		default:
			return ToolChoice{}, invalid("invalid_parameter", param, "The tool_choice value is not supported.")
		}
	}
	var choice map[string]json.RawMessage
	if json.Unmarshal(raw, &choice) != nil || choice == nil {
		return ToolChoice{}, invalid("invalid_parameter", param, "The tool_choice parameter is malformed.")
	}
	choiceType := "function"
	if rawType, ok := choice["type"]; ok && json.Unmarshal(rawType, &choiceType) != nil {
		return ToolChoice{}, invalid("invalid_parameter", param+".type", "The tool choice type must be a string.")
	}
	if choiceType != "function" {
		return ToolChoice{}, invalid("invalid_parameter", param+".type", "Only function tool choices are supported.")
	}
	name := stringValue(choice["name"])
	if name == "" {
		if nested, ok := choice["function"]; ok {
			var function map[string]json.RawMessage
			if json.Unmarshal(nested, &function) == nil {
				name = stringValue(function["name"])
			}
		}
	}
	if name == "" {
		return ToolChoice{}, invalid("missing_required_parameter", param+".name", "A function tool choice requires a name.")
	}
	if len(names) > 0 {
		if _, ok := names[name]; !ok {
			return ToolChoice{}, invalid("invalid_parameter", param+".name", "The selected function is not present in tools.")
		}
	}
	return ToolChoice{Mode: "function", Name: name}, nil
}

func MarshalToolChoice(choice ToolChoice) ([]byte, error) {
	if choice.Mode == "" {
		return nil, nil
	}
	if choice.Mode != "function" {
		return json.Marshal(choice.Mode)
	}
	return json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": choice.Name}})
}

func NormalizeArguments(raw json.RawMessage, param string) (string, error) {
	if len(raw) == 0 || isNull(raw) {
		return "{}", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return "{}", nil
		}
		if !json.Valid([]byte(text)) {
			return "", invalid("invalid_parameter", param, "Tool arguments must contain valid JSON.")
		}
		return compactJSON([]byte(text)), nil
	}
	if !json.Valid(raw) {
		return "", invalid("invalid_parameter", param, "Tool arguments must contain valid JSON.")
	}
	return compactJSON(raw), nil
}

func ToolNames(definitions []ToolDefinition) map[string]struct{} {
	names := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		names[definition.Name] = struct{}{}
	}
	return names
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

func compactJSON(value []byte) string {
	var output bytes.Buffer
	if err := json.Compact(&output, value); err != nil {
		return string(bytes.TrimSpace(value))
	}
	return output.String()
}

func isNull(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func stringValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func sortedToolIndexes[T any](items map[int]T) []int {
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}
