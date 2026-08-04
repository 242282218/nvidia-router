package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"nvidia-router/internal/apierror"
)

var validRoles = map[string]struct{}{
	"system":    {},
	"developer": {},
	"user":      {},
	"assistant": {},
	"tool":      {},
}

func validateMessages(fields map[string]json.RawMessage) (bool, bool, error) {
	raw, ok := fields["messages"]
	if !ok {
		return false, false, invalidRequest("missing_required_parameter", "messages", "The messages parameter is required.")
	}
	scanner := newJSONScanner(raw)
	opened, err := consumeJSONOpening(scanner, '[')
	if err != nil || !opened {
		return false, false, invalidRequest("invalid_parameter", "messages", "The messages parameter must be a non-empty array.")
	}
	hasImage := false
	hasTools := false
	messageCount := 0
	for scanner.more(']') {
		message, err := parseMessage(scanner, messageCount)
		if err != nil {
			if requestError, ok := err.(*apierror.Error); ok {
				return false, false, requestError
			}
			return false, false, invalidRequest("invalid_parameter", "messages", "The messages parameter must be a non-empty array.")
		}
		found, requiresTools, err := validateMessage(message, messageCount)
		if err != nil {
			return false, false, err
		}
		hasImage = hasImage || found
		hasTools = hasTools || requiresTools
		messageCount++
	}
	if err := consumeJSONClosing(scanner, ']'); err != nil || messageCount == 0 {
		return false, false, invalidRequest("invalid_parameter", "messages", "The messages parameter must be a non-empty array.")
	}
	return hasImage, hasTools, nil
}

func validateMessage(message parsedMessage, index int) (bool, bool, error) {
	if !message.valid {
		return false, false, invalidRequest("invalid_parameter", fmt.Sprintf("messages[%d]", index), "Each message must be an object.")
	}
	param := fmt.Sprintf("messages[%d].role", index)
	if !message.role.present {
		return false, false, invalidRequest("missing_required_parameter", param, "Each message must include a role.")
	}
	if message.roleAlias || !message.role.valid {
		return false, false, invalidRequest("invalid_parameter", param, "The message role must be a supported string.")
	}
	if _, ok := validRoles[message.role.value]; !ok {
		return false, false, invalidRequest("invalid_parameter", param, "The message role is not supported.")
	}
	if message.contentAlias {
		return false, false, invalidRequest("invalid_parameter", fmt.Sprintf("messages[%d].content", index), "The content field is ambiguous.")
	}
	if message.toolCallsAlias {
		return false, false, invalidRequest("invalid_parameter", fmt.Sprintf("messages[%d].tool_calls", index), "The tool_calls field is ambiguous.")
	}
	if message.toolCalls.present && !message.toolCalls.valid {
		return false, false, invalidRequest("invalid_parameter", fmt.Sprintf("messages[%d].tool_calls", index), "The tool_calls parameter must be an array.")
	}
	vision, err := validateMessageContent(message.content, index)
	if err != nil {
		return false, false, err
	}
	requiresTools := message.role.value == "tool" ||
		message.role.value == "assistant" && message.toolCalls.present && message.toolCalls.length > 0
	return vision, requiresTools, nil
}

func optionalStream(fields map[string]json.RawMessage) (bool, error) {
	raw, ok := fields["stream"]
	if !ok {
		return false, nil
	}
	var stream *bool
	if json.Unmarshal(raw, &stream) != nil || stream == nil {
		return false, invalidRequest("invalid_parameter", "stream", "The stream parameter must be a boolean.")
	}
	return *stream, nil
}

func validateTools(fields map[string]json.RawMessage) (bool, error) {
	raw, ok := fields["tools"]
	if !ok {
		return false, nil
	}
	scanner := newJSONScanner(raw)
	opened, err := consumeJSONOpening(scanner, '[')
	if err != nil || !opened {
		return false, invalidRequest("invalid_parameter", "tools", "The tools parameter must be an array.")
	}
	toolCount := 0
	for scanner.more(']') {
		if err := validateTool(scanner, toolCount); err != nil {
			return false, err
		}
		toolCount++
	}
	if err := consumeJSONClosing(scanner, ']'); err != nil {
		return false, invalidRequest("invalid_parameter", "tools", "The tools parameter must be an array.")
	}
	return toolCount > 0, nil
}

func validateTool(scanner *jsonScanner, index int) error {
	typeParam := fmt.Sprintf("tools[%d].type", index)
	nameParam := fmt.Sprintf("tools[%d].function.name", index)
	tool, err := parseToolObject(scanner, typeParam, nameParam)
	if err != nil {
		return err
	}
	if !tool.valid || tool.typeAlias || !tool.toolType.valid || tool.toolType.value != "function" {
		return invalidRequest("invalid_parameter", typeParam, "Only function tools are supported.")
	}
	if !tool.function.present && !tool.functionAlias {
		return invalidRequest("missing_required_parameter", nameParam, "A function tool name is required.")
	}
	if tool.functionAlias || !tool.function.valid || tool.function.nameAlias {
		return invalidRequest("invalid_parameter", nameParam, "A function tool name must be a non-empty string.")
	}
	if !tool.function.name.present {
		return invalidRequest("missing_required_parameter", nameParam, "A function tool name is required.")
	}
	if !tool.function.name.valid || tool.function.name.value == "" {
		return invalidRequest("invalid_parameter", nameParam, "A function tool name must be a non-empty string.")
	}
	return nil
}

type parsedTool struct {
	valid         bool
	toolType      jsonString
	typeAlias     bool
	function      namedFunction
	functionAlias bool
}

type namedFunction struct {
	present   bool
	valid     bool
	name      jsonString
	nameAlias bool
}

func parseToolObject(scanner *jsonScanner, typeParam, functionParam string) (parsedTool, error) {
	var tool parsedTool
	object, err := consumeJSONOpening(scanner, '{')
	if err != nil || !object {
		return tool, err
	}
	tool.valid = true
	var seenType, seenFunction bool
	for scanner.more('}') {
		key, err := nextJSONObjectKey(scanner)
		if err != nil {
			return tool, err
		}
		if err := parseToolField(scanner, key, &tool, &seenType, &seenFunction, typeParam, functionParam); err != nil {
			return tool, err
		}
	}
	return tool, consumeJSONClosing(scanner, '}')
}

func parseToolField(
	scanner *jsonScanner,
	key string,
	tool *parsedTool,
	seenType, seenFunction *bool,
	typeParam, functionParam string,
) error {
	switch key {
	case "type":
		if *seenType {
			return duplicateJSONField(scanner, typeParam)
		}
		*seenType = true
		value, err := readJSONString(scanner)
		tool.toolType = value
		return err
	case "function":
		if *seenFunction {
			return duplicateJSONField(scanner, functionParam)
		}
		*seenFunction = true
		value, err := parseNamedFunction(scanner, functionParam)
		tool.function = value
		return err
	default:
		tool.typeAlias = tool.typeAlias || strings.EqualFold(key, "type")
		tool.functionAlias = tool.functionAlias || strings.EqualFold(key, "function")
		return skipJSONValue(scanner)
	}
}

func parseNamedFunction(scanner *jsonScanner, nameParam string) (namedFunction, error) {
	function := namedFunction{present: true}
	object, err := consumeJSONOpening(scanner, '{')
	if err != nil || !object {
		return function, err
	}
	function.valid = true
	seenName := false
	for scanner.more('}') {
		key, err := nextJSONObjectKey(scanner)
		if err != nil {
			return function, err
		}
		if key != "name" {
			function.nameAlias = function.nameAlias || strings.EqualFold(key, "name")
			if err := skipJSONValue(scanner); err != nil {
				return function, err
			}
			continue
		}
		if seenName {
			return function, duplicateJSONField(scanner, nameParam)
		}
		seenName = true
		function.name, err = readJSONString(scanner)
		if err != nil {
			return function, err
		}
	}
	return function, consumeJSONClosing(scanner, '}')
}

func validateToolChoice(fields map[string]json.RawMessage) (bool, error) {
	raw, ok := fields["tool_choice"]
	if !ok {
		return false, nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if value == "none" {
			return false, nil
		}
		if value == "auto" || value == "required" {
			return true, nil
		}
		return false, invalidRequest("invalid_parameter", "tool_choice", "The tool_choice value is not supported.")
	}
	choice, err := parseToolObject(newJSONScanner(raw), "tool_choice", "tool_choice")
	if err == nil && validNamedToolChoice(choice) {
		return true, nil
	}
	if requestError, ok := err.(*apierror.Error); ok {
		return false, requestError
	}
	return false, invalidRequest("invalid_parameter", "tool_choice", "The tool_choice parameter must select a function or a supported mode.")
}

func validNamedToolChoice(choice parsedTool) bool {
	return choice.valid && !choice.typeAlias && choice.toolType.valid && choice.toolType.value == "function" &&
		!choice.functionAlias && choice.function.valid && !choice.function.nameAlias &&
		choice.function.name.valid && choice.function.name.value != ""
}
