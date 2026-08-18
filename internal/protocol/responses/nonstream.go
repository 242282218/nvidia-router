package responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nvidia-router/internal/compat"
	"nvidia-router/internal/modelcatalog"
)

// FromChat converts a validated OpenAI Chat Completions non-stream response body
// into a Responses API response body. Only the first choice is represented; the
// remaining Chat response fields are not forwarded because Responses semantics
// are reconstructed, not relayed.
func FromChat(chatBody []byte, responsesID string, model modelcatalog.Model) ([]byte, error) {
	return FromChatWithConfig(chatBody, responsesID, model, ResponseConfig{})
}

func FromChatWithConfig(chatBody []byte, responsesID string, model modelcatalog.Model, config ResponseConfig) ([]byte, error) {
	if strings.TrimSpace(responsesID) == "" {
		return nil, fmt.Errorf("convert chat to responses: response id is required")
	}
	var chat struct {
		Choices []struct {
			Message      chatChoiceMessage `json:"message"`
			FinishReason string            `json:"finish_reason"`
		} `json:"choices"`
		Usage *chatUsage `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		return nil, fmt.Errorf("convert chat to responses: decode chat body: %w", err)
	}
	if len(chat.Choices) == 0 {
		return nil, fmt.Errorf("convert chat to responses: chat body has no choices")
	}

	output, err := buildOutputItems(chat.Choices[0].Message, responsesID)
	if err != nil {
		return nil, err
	}

	usage := convertUsage(chat.Usage)
	response := map[string]any{
		"id":         responsesID,
		"object":     "response",
		"status":     statusForFinishReason(chat.Choices[0].FinishReason),
		"created_at": time.Now().Unix(),
		"model":      model.PublicID,
		"output":     output,
		// output_text is the flattened convenience accessor the SDKs expose as
		// response.output_text. Without it, callers that read it instead of
		// walking output see an empty result.
		"output_text": outputText(output),
	}
	config.apply(response)
	// incomplete_details is only meaningful for an incomplete response; the spec
	// leaves it absent otherwise.
	if reason, ok := incompleteReason(chat.Choices[0].FinishReason); ok {
		response["incomplete_details"] = map[string]any{"reason": reason}
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
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ReasoningContent json.RawMessage `json:"reasoning_content"`
	Reasoning        json.RawMessage `json:"reasoning"`
	Thinking         json.RawMessage `json:"thinking"`
	ToolCalls        []struct {
		ID       string          `json:"id"`
		Type     string          `json:"type"`
		Function toolCallContent `json:"function"`
	} `json:"tool_calls"`
}

type toolCallContent struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatUsage = ChatUsage

func extractReasoning(message chatChoiceMessage) (string, bool, error) {
	var result string
	found := false
	for _, item := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "reasoning_content", raw: message.ReasoningContent},
		{name: "reasoning", raw: message.Reasoning},
		{name: "thinking", raw: message.Thinking},
	} {
		if len(item.raw) == 0 || string(item.raw) == "null" {
			continue
		}
		text, present, err := extractText(item.raw, item.name)
		if err != nil {
			return "", false, err
		}
		if !present {
			continue
		}
		if found && result != text {
			return "", false, fmt.Errorf("convert chat to responses: reasoning aliases disagree")
		}
		result, found = text, true
	}
	return result, found, nil
}

func buildOutputItems(message chatChoiceMessage, responsesID string) ([]map[string]any, error) {
	output := make([]map[string]any, 0, 2+len(message.ToolCalls))
	// Reasoning precedes tool calls and text in a Responses output, matching
	// how thinking models emit chain-of-thought before the answer.
	reasoning, hasReasoning, err := extractReasoning(message)
	if err != nil {
		return nil, err
	}
	if hasReasoning {
		output = append(output, map[string]any{
			"id": reasoningItemID(responsesID), "type": "reasoning", "status": "completed",
			"summary": []map[string]any{{"type": "summary_text", "text": reasoning}},
		})
	}
	for index, call := range message.ToolCalls {
		id := call.ID
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("call_%d", index)
		}
		arguments, err := compat.NormalizeArguments(call.Function.Arguments, fmt.Sprintf("tool_calls[%d].function.arguments", index))
		if err != nil {
			return nil, fmt.Errorf("convert chat to responses: %w", err)
		}
		output = append(output, map[string]any{
			"type":      "function_call",
			"status":    "completed",
			"id":        id,
			"call_id":   id,
			"name":      call.Function.Name,
			"arguments": arguments,
		})
	}
	text, hasText, err := extractText(message.Content, "content")
	if err != nil {
		return nil, err
	}
	if hasText {
		output = append(output, map[string]any{
			"id": messageItemID(responsesID), "type": "message", "status": "completed",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}},
			},
		})
	}
	return output, nil
}

// extractText pulls the string value from a Chat message field that may be a
// plain string, null or an array of content parts. label names the field for
// error messages.
func extractText(content json.RawMessage, label string) (string, bool, error) {
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
		return "", false, fmt.Errorf("convert chat to responses: assistant %s must be text", label)
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

// statusForFinishReason maps the Chat finish_reason onto a Responses status.
// Reporting completed unconditionally hid truncation: a response cut off by the
// token limit looked identical to one that finished normally.
func statusForFinishReason(reason string) string {
	switch reason {
	case "length", "content_filter":
		return "incomplete"
	default:
		return "completed"
	}
}

// incompleteReason returns the Responses incomplete_details reason for a Chat
// finish_reason, and false when the response is not incomplete.
func incompleteReason(reason string) (string, bool) {
	switch reason {
	case "length":
		return "max_output_tokens", true
	case "content_filter":
		return "content_filter", true
	default:
		return "", false
	}
}

// outputText flattens the assistant text across output items, mirroring the
// SDK-level response.output_text accessor.
func outputText(output []map[string]any) string {
	var builder strings.Builder
	for _, item := range output {
		if item["type"] != "message" {
			continue
		}
		parts, ok := item["content"].([]map[string]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			if part["type"] != "output_text" {
				continue
			}
			if text, ok := part["text"].(string); ok {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}
