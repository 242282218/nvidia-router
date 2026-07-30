package responses

import (
	"bytes"
	"encoding/json"
)

// chatTranscript mirrors only the subset of the emitted Chat request used by
// request tests: exact role/content/tool structure plus the mapped scalar
// fields. It asserts protocol semantics without coupling to the Chat package.
type chatTranscript struct {
	Model           string                     `json:"model"`
	Stream          *bool                      `json:"stream,omitempty"`
	Tools           []rawTool                  `json:"tools,omitempty"`
	ToolChoice      []byte                     `json:"tool_choice,omitempty"`
	ReasoningEffort *string                    `json:"reasoning_effort,omitempty"`
	MaxTokens       *int                       `json:"max_tokens,omitempty"`
	Messages        []chatMessage              `json:"messages"`
	Raw             map[string]json.RawMessage `json:"-"`
}

type chatMessage struct {
	Role              string         `json:"role"`
	RolePresent       bool           `json:"-"`
	Content           string         `json:"content,omitempty"`
	ContentPresent    bool           `json:"-"`
	ToolCallID        string         `json:"tool_call_id,omitempty"`
	ToolCallIDPresent bool           `json:"-"`
	ToolCalls         []chatToolCall `json:"tool_calls,omitempty"`
}

type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type rawTool struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function,omitempty"`
}

type responsesInputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

func decodeTranscriptForTest(payload []byte) (chatTranscript, error) {
	var transcript chatTranscript
	raw := map[string]json.RawMessage{}
	_ = json.Unmarshal(payload, &raw)
	transcript.Raw = raw
	if err := json.Unmarshal(payload, &transcript); err != nil {
		return transcript, err
	}
	// Presence detection requires comparing against the raw object because
	// json omits empty strings and zero values.
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	for index := range transcript.Messages {
		if rawMsg := extractMessageRaw(raw, index); rawMsg != nil {
			fillPresent(&transcript.Messages[index], rawMsg)
		}
	}
	return transcript, nil
}

func extractMessageRaw(root map[string]json.RawMessage, index int) json.RawMessage {
	rawMessages, ok := root["messages"]
	if !ok {
		return nil
	}
	var arr []json.RawMessage
	if json.Unmarshal(rawMessages, &arr) != nil || index >= len(arr) {
		return nil
	}
	return arr[index]
}

func fillPresent(message *chatMessage, raw json.RawMessage) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return
	}
	if _, ok := fields["role"]; ok {
		message.RolePresent = true
	}
	if _, ok := fields["content"]; ok {
		message.ContentPresent = true
	}
	if _, ok := fields["tool_call_id"]; ok {
		message.ToolCallIDPresent = true
	}
}
