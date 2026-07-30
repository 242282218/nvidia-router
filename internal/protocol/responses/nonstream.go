package responses

import (
	"encoding/json"
	"fmt"
	"strings"

	"nvidia-router/internal/modelcatalog"
)

// FromChat converts a validated OpenAI Chat Completions non-stream response body
// into a Responses API response body. Only the first choice is represented; the
// remaining Chat response fields are not forwarded because Responses semantics
// are reconstructed, not relayed.
func FromChat(chatBody []byte, responsesID string, model modelcatalog.Model) ([]byte, error) {
	if strings.TrimSpace(responsesID) == "" {
		return nil, fmt.Errorf("convert chat to responses: response id is required")
	}
	var chat struct {
		Choices []struct {
			Message chatChoiceMessage `json:"message"`
		} `json:"choices"`
		Usage *chatUsage `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		return nil, fmt.Errorf("convert chat to responses: decode chat body: %w", err)
	}
	if len(chat.Choices) == 0 {
		return nil, fmt.Errorf("convert chat to responses: chat body has no choices")
	}

	output, err := buildOutputItems(chat.Choices[0].Message)
	if err != nil {
		return nil, err
	}

	usage := convertUsage(chat.Usage)
	response := map[string]any{
		"id":     responsesID,
		"object": "response",
		"status": "completed",
		"model":  model.PublicID,
		"output": output,
	}
	if usage != nil {
		usageBytes, err := json.Marshal(usage)
		if err != nil {
			return nil, fmt.Errorf("convert chat to responses: marshal usage: %w", err)
		}
		response["usage"] = json.RawMessage(usageBytes)
	}
	bytes, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("convert chat to responses: marshal response: %w", err)
	}
	return bytes, nil
}

type chatChoiceMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []struct {
		ID       string          `json:"id"`
		Type     string          `json:"type"`
		Function toolCallContent `json:"function"`
	} `json:"tool_calls"`
}

type toolCallContent struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatUsage struct {
	PromptTokens     *int `json:"prompt_tokens,omitempty"`
	CompletionTokens *int `json:"completion_tokens,omitempty"`
	TotalTokens      *int `json:"total_tokens,omitempty"`
}

func buildOutputItems(message chatChoiceMessage) ([]map[string]any, error) {
	output := make([]map[string]any, 0, 1+len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        call.ID,
			"call_id":   call.ID,
			"name":      call.Function.Name,
			"arguments": call.Function.Arguments,
		})
	}
	text, hasText, err := extractAssistantText(message.Content)
	if err != nil {
		return nil, err
	}
	if hasText {
		output = append(output, map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": text},
			},
		})
	}
	return output, nil
}

func extractAssistantText(content json.RawMessage) (string, bool, error) {
	if len(content) == 0 || string(content) == "null" {
		return "", false, nil
	}
	var asString string
	if json.Unmarshal(content, &asString) == nil {
		if asString == "" {
			return "", false, nil
		}
		return asString, true, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return "", false, fmt.Errorf("convert chat to responses: assistant content must be text")
	}
	var text string
	for _, part := range parts {
		text += part.Text
	}
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

func convertUsage(usage *chatUsage) map[string]any {
	if usage == nil {
		return nil
	}
	result := map[string]any{}
	if usage.PromptTokens != nil {
		result["input_tokens"] = *usage.PromptTokens
	}
	if usage.CompletionTokens != nil {
		result["output_tokens"] = *usage.CompletionTokens
	}
	if usage.TotalTokens != nil {
		result["total_tokens"] = *usage.TotalTokens
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
