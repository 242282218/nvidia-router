package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"nvidia-router/internal/compat"
)

func normalizeChatMessages(raw json.RawMessage) (json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || messages == nil {
		return nil, invalidRequest("invalid_parameter", "messages", "The messages parameter must be an array.")
	}
	pendingByName := make(map[string][]string)
	for messageIndex, message := range messages {
		role := stringValue(message["role"])
		switch role {
		case "assistant":
			calls, present, err := normalizeAssistantCalls(message, messageIndex)
			if err != nil {
				return nil, err
			}
			if present {
				encoded, _ := json.Marshal(calls)
				message["tool_calls"] = encoded
				delete(message, "function_call")
				for _, call := range calls {
					function := asObject(call["function"])
					name := stringValue(function["name"])
					id := stringValue(call["id"])
					pendingByName[name] = append(pendingByName[name], id)
				}
			}
		case "function":
			if err := normalizeLegacyFunctionResult(message, messageIndex, pendingByName); err != nil {
				return nil, err
			}
		case "tool":
			if err := normalizeToolResult(message, messageIndex); err != nil {
				return nil, err
			}
		}
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized chat messages: %w", err)
	}
	return encoded, nil
}

func normalizeAssistantCalls(message map[string]json.RawMessage, messageIndex int) ([]map[string]json.RawMessage, bool, error) {
	if rawCalls, ok := message["tool_calls"]; ok {
		calls, err := normalizeToolCalls(rawCalls, fmt.Sprintf("messages[%d].tool_calls", messageIndex))
		return calls, true, err
	}
	legacy, ok := message["function_call"]
	if !ok {
		return nil, false, nil
	}
	calls, err := normalizeToolCalls(json.RawMessage("["+string(legacy)+"]"), fmt.Sprintf("messages[%d].function_call", messageIndex))
	return calls, true, err
}

func normalizeToolCalls(raw json.RawMessage, param string) ([]map[string]json.RawMessage, error) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return nil, invalidRequest("invalid_parameter", param, "Tool calls must be an array.")
	}
	calls := make([]map[string]json.RawMessage, 0, len(items))
	for index, item := range items {
		if item == nil {
			return nil, invalidRequest("invalid_parameter", fmt.Sprintf("%s[%d]", param, index), "Each tool call must be an object.")
		}
		call, err := normalizeToolCall(item, index, fmt.Sprintf("%s[%d]", param, index))
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func normalizeToolCall(item map[string]json.RawMessage, index int, param string) (map[string]json.RawMessage, error) {
	callType := stringValue(item["type"])
	if callType == "" {
		callType = "function"
	}
	if callType != "function" {
		return nil, invalidRequest("invalid_parameter", param+".type", "Only function tool calls are supported.")
	}
	function := asObject(item["function"])
	if function == nil {
		function = make(map[string]json.RawMessage)
		for _, name := range []string{"name", "arguments"} {
			if value, ok := item[name]; ok {
				function[name] = value
			}
		}
	}
	name := stringValue(function["name"])
	if name == "" {
		return nil, invalidRequest("missing_required_parameter", param+".function.name", "A tool call function name is required.")
	}
	arguments, err := compat.NormalizeArguments(function["arguments"], param+".function.arguments")
	if err != nil {
		return nil, compatRequestError(err)
	}
	id := stringValue(item["id"])
	if id == "" {
		id = stringValue(item["call_id"])
	}
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("call_%d", index)
	}
	encodedID, _ := json.Marshal(id)
	encodedName, _ := json.Marshal(name)
	encodedArguments, _ := json.Marshal(arguments)
	return map[string]json.RawMessage{
		"id": encodedID, "type": json.RawMessage(`"function"`),
		"function": mustJSON(map[string]json.RawMessage{"name": encodedName, "arguments": encodedArguments}),
	}, nil
}

func normalizeLegacyFunctionResult(message map[string]json.RawMessage, messageIndex int, pendingByName map[string][]string) error {
	name := stringValue(message["name"])
	if name == "" {
		return invalidRequest("missing_required_parameter", fmt.Sprintf("messages[%d].name", messageIndex), "A legacy function result requires a name.")
	}
	id := stringValue(message["tool_call_id"])
	if id == "" {
		id = popPendingCall(pendingByName, name)
	}
	if id == "" {
		id = fmt.Sprintf("call_%d", messageIndex)
	}
	message["role"] = json.RawMessage(`"tool"`)
	encodedID, _ := json.Marshal(id)
	message["tool_call_id"] = encodedID
	delete(message, "name")
	delete(message, "call_id")
	return normalizeToolResult(message, messageIndex)
}

func normalizeToolResult(message map[string]json.RawMessage, messageIndex int) error {
	if _, ok := message["tool_call_id"]; !ok {
		if rawID, hasCallID := message["call_id"]; hasCallID {
			message["tool_call_id"] = rawID
			delete(message, "call_id")
		}
	}
	if rawContent, ok := message["content"]; ok {
		content, err := compat.FlattenToolOutput(rawContent, fmt.Sprintf("messages[%d].content", messageIndex))
		if err != nil {
			return compatRequestError(err)
		}
		encoded, _ := json.Marshal(content)
		message["content"] = encoded
	}
	return nil
}

func popPendingCall(pendingByName map[string][]string, name string) string {
	ids := pendingByName[name]
	if len(ids) == 0 {
		return ""
	}
	id := ids[0]
	if len(ids) == 1 {
		delete(pendingByName, name)
	} else {
		pendingByName[name] = ids[1:]
	}
	return id
}

func asObject(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func stringValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}
