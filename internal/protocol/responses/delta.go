package responses

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseChatDelta decodes a single Chat Completions streaming chunk's data
// payload into the projection the state machine consumes. Comments, event
// names and the trailing [DONE] marker are handled by the SSE layer; this
// function only interprets one JSON data frame. It is a pure function so it can
// be unit tested without HTTP plumbing. done reports whether the frame is the
// terminal [DONE] marker rather than a delta.
func ParseChatDelta(data []byte) (delta ChatDelta, done bool, err error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "[DONE]" {
		return ChatDelta{}, true, nil
	}
	if trimmed == "" {
		return ChatDelta{}, false, nil
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Role      string              `json:"role"`
				Content   json.RawMessage     `json:"content"`
				Reasoning json.RawMessage     `json:"reasoning_content"`
				ToolCalls []chatChunkToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason json.RawMessage `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return ChatDelta{}, false, fmt.Errorf("decode chat delta: %w", err)
	}
	parsed := ChatDelta{}
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		parsed.Content = decodeStringField(choice.Delta.Content)
		reasoning := decodeStringField(choice.Delta.Reasoning)
		if reasoning != "" {
			parsed.Reasoning = reasoning
		}
		for _, call := range choice.Delta.ToolCalls {
			parsed.ToolCalls = append(parsed.ToolCalls, ChatToolCallDelta{
				Index:     call.Index,
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
		parsed.FinishReason = decodeStringField(choice.FinishReason)
	}
	return parsed, false, nil
}

type chatChunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// decodeStringField returns the string value of a JSON field that may be a
// string, null or absent. When the field is an array of content parts (the
// OpenAI-compatible shape some upstreams stream, e.g.
// [{"type":"text","text":"..."}]), the text parts are concatenated so content
// deltas are not silently dropped.
func decodeStringField(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}
