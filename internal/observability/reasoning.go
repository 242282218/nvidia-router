package observability

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"nvidia-router/internal/compat"
)

// reasoningWireFieldOrder is the fixed order in which reasoning field names are
// joined into ReasoningWireFields so the value is deterministic across requests.
var reasoningWireFieldOrder = []string{"reasoning_effort", "reasoning", "thinking"}

// ReasoningFieldsFromBody inspects only the top-level field names of a marshaled
// upstream body to decide whether reasoning was requested and which field names
// carried that intent. Values are never read or retained; a null-valued
// reasoning field still counts as requested because the client explicitly opted
// into thinking.
func ReasoningFieldsFromBody(body []byte) (requested bool, wireFields string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false, ""
	}
	names := make([]string, 0, len(reasoningWireFieldOrder))
	for _, name := range reasoningWireFieldOrder {
		if _, ok := fields[name]; ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return false, ""
	}
	return true, strings.Join(names, ",")
}

// ReasoningLevelFromBody extracts only the normalized reasoning level from a
// request body. It deliberately discards all reasoning values and text.
func ReasoningLevelFromBody(body []byte) (string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return "", false
	}
	spec, err := compat.ParseReasoning(fields)
	if err != nil || !spec.Requested || spec.Level == "" {
		return "", false
	}
	return string(spec.Level), true
}

// ReasoningContentFromBody reports whether a non-stream chat body carries
// assistant reasoning_content, reasoning, or thinking and its total character count (runes).
// Only the length is returned; the reasoning text itself is never retained.
func ReasoningContentFromBody(body []byte) (present bool, chars int64) {
	var chat struct {
		Choices []struct {
			Message struct {
				ReasoningContent json.RawMessage `json:"reasoning_content"`
				Reasoning        json.RawMessage `json:"reasoning"`
				Thinking         json.RawMessage `json:"thinking"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chat); err != nil {
		return false, 0
	}
	for _, choice := range chat.Choices {
		length, ok := firstReasoningLength(choice.Message.ReasoningContent, choice.Message.Reasoning, choice.Message.Thinking)
		if !ok {
			continue
		}
		chars += length
		present = true
	}
	return present, chars
}

// ReasoningDeltaChars reports whether a single SSE chat delta frame carries
// reasoning_content, reasoning, or thinking and its character count (runes). Only the length is
// returned; reasoning text is never retained.
func ReasoningDeltaChars(data []byte) (present bool, chars int64) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				ReasoningContent json.RawMessage `json:"reasoning_content"`
				Reasoning        json.RawMessage `json:"reasoning"`
				Thinking         json.RawMessage `json:"thinking"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return false, 0
	}
	for _, choice := range chunk.Choices {
		length, ok := firstReasoningLength(choice.Delta.ReasoningContent, choice.Delta.Reasoning, choice.Delta.Thinking)
		if !ok {
			continue
		}
		chars += length
		present = true
	}
	return present, chars
}

func firstReasoningLength(values ...json.RawMessage) (int64, bool) {
	for _, value := range values {
		if length, ok := reasoningContentLength(value); ok {
			return length, true
		}
	}
	return 0, false
}

// reasoningContentLength returns the rune count of a reasoning field
// that may be a string, structured object (thought/text), null or absent, and whether any text was present.
func reasoningContentLength(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return int64(utf8.RuneCountInString(text)), true
	}
	var nested struct {
		Thought string `json:"thought"`
		Text    string `json:"text"`
	}
	if json.Unmarshal(raw, &nested) == nil {
		if nested.Thought != "" {
			return int64(utf8.RuneCountInString(nested.Thought)), true
		}
		if nested.Text != "" {
			return int64(utf8.RuneCountInString(nested.Text)), true
		}
	}
	return 0, false
}
