package responses

import "encoding/json"

// ResponseConfig carries request-owned fields repeated on a Responses result.
// Raw JSON preserves null and scalar values without retaining the full request.
type ResponseConfig struct {
	Instructions      json.RawMessage
	MaxOutputTokens   json.RawMessage
	ParallelToolCalls json.RawMessage
	Reasoning         json.RawMessage
	Temperature       json.RawMessage
	Text              json.RawMessage
	ToolChoice        json.RawMessage
	Tools             json.RawMessage
	TopP              json.RawMessage
	User              json.RawMessage
}

func responseConfigFromFields(fields map[string]json.RawMessage, toolNames map[string]struct{}) (ResponseConfig, error) {
	tools, err := normaliseResponseTools(fields["tools"])
	if err != nil {
		return ResponseConfig{}, err
	}
	toolChoice, err := normaliseResponseToolChoice(fields["tool_choice"], toolNames)
	if err != nil {
		return ResponseConfig{}, err
	}
	return ResponseConfig{
		Instructions:      cloneRaw(fields["instructions"]),
		MaxOutputTokens:   cloneRaw(fields["max_output_tokens"]),
		ParallelToolCalls: cloneRaw(fields["parallel_tool_calls"]),
		Reasoning:         cloneRaw(fields["reasoning"]),
		Temperature:       cloneRaw(fields["temperature"]),
		Text:              cloneRaw(fields["text"]),
		ToolChoice:        toolChoice,
		Tools:             tools,
		TopP:              cloneRaw(fields["top_p"]),
		User:              cloneRaw(fields["user"]),
	}, nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func (c ResponseConfig) apply(response map[string]any) {
	response["error"] = nil
	response["incomplete_details"] = nil
	response["instructions"] = rawValue(c.Instructions)
	response["max_output_tokens"] = rawValue(c.MaxOutputTokens)
	response["metadata"] = nil
	if len(c.ParallelToolCalls) == 0 || string(c.ParallelToolCalls) == "null" {
		response["parallel_tool_calls"] = true
	} else {
		response["parallel_tool_calls"] = rawValue(c.ParallelToolCalls)
	}
	response["temperature"] = rawValue(c.Temperature)
	response["reasoning"] = rawValue(c.Reasoning)
	response["text"] = rawValue(c.Text)
	response["tool_choice"] = rawValue(c.ToolChoice)
	response["tools"] = rawValue(c.Tools)
	response["top_p"] = rawValue(c.TopP)
	response["user"] = rawValue(c.User)
}

func rawValue(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}
