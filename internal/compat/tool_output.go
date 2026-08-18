package compat

import (
	"encoding/json"
	"fmt"
	"strings"
)

const UnsupportedImageOutput = "[image omitted: unsupported by upstream]"

func FlattenToolOutput(raw json.RawMessage, param string) (string, error) {
	if len(raw) == 0 {
		return "", invalid("missing_required_parameter", param, "Tool output is required.")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	if isNull(raw) {
		return "null", nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		values := make([]string, 0, len(parts))
		for index, part := range parts {
			value, err := flattenOutputPart(part, fmt.Sprintf("%s[%d]", param, index))
			if err != nil {
				return "", err
			}
			values = append(values, value)
		}
		return strings.Join(values, "\n\n"), nil
	}
	if !json.Valid(raw) {
		return "", invalid("invalid_parameter", param, "Tool output must be valid JSON, a string, or text parts.")
	}
	return compactJSON(raw), nil
}

func flattenOutputPart(raw json.RawMessage, param string) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var part map[string]json.RawMessage
	if json.Unmarshal(raw, &part) != nil || part == nil {
		if !json.Valid(raw) {
			return "", invalid("invalid_parameter", param, "Tool output parts must be valid JSON.")
		}
		return compactJSON(raw), nil
	}
	typeName := strings.ToLower(stringValue(part["type"]))
	switch typeName {
	case "input_image", "output_image", "image", "image_url", "file", "input_file", "output_file":
		return UnsupportedImageOutput, nil
	case "input_text", "output_text", "text", "":
		if rawText, ok := part["text"]; ok {
			if json.Unmarshal(rawText, &text) != nil {
				return "", invalid("invalid_parameter", param+".text", "Text tool output parts require a string text field.")
			}
			return text, nil
		}
	}
	return compactJSON(raw), nil
}
