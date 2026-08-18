package responses

import (
	"encoding/json"
	"errors"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/compat"
)

// Four Responses statuses share the same public rejection code so clients see a
// stable contract for every feature the router deliberately does not proxy.
func unsupportedResponses(param, message string) error {
	return invalidResponses("unsupported_responses_feature", param, message)
}

func invalidResponses(code, param, message string) error {
	var parameter *string
	if param != "" {
		parameter = &param
	}
	return &apierror.Error{
		Status: 400, Type: "invalid_request_error", Code: code, Message: message, Param: parameter,
	}
}

func compatRequestError(err error) error {
	var validation *compat.ValidationError
	if errors.As(err, &validation) {
		return invalidResponses(validation.Code, validation.Param, validation.Message)
	}
	return invalidResponses("invalid_parameter", "", err.Error())
}

func reasoningResponseModelError(err error) error {
	if errors.Is(err, compat.ErrReasoningUnsupported) {
		return invalidResponses("model_capability_unsupported", "reasoning", "The selected model does not support the requested reasoning mode.")
	}
	return compatRequestError(err)
}

type topLevelCheck struct {
	reject func(map[string]json.RawMessage) error
}

func rejectUnsupportedTopLevel(fields map[string]json.RawMessage) error {
	allowed := map[string]struct{}{
		"model": {}, "input": {}, "instructions": {}, "stream": {}, "tools": {}, "tool_choice": {},
		"parallel_tool_calls": {}, "max_output_tokens": {}, "reasoning": {}, "reasoning_effort": {},
		"text": {}, "temperature": {}, "top_p": {}, "user": {}, "store": {}, "background": {},
		"previous_response_id": {}, "conversation": {}, "metadata": {}, "prompt": {}, "prompt_cache_key": {},
		"prompt_cache_options": {}, "prompt_cache_retention": {}, "context_management": {}, "include": {},
		"max_tool_calls": {}, "moderation": {}, "safety_identifier": {}, "service_tier": {}, "truncation": {},
		"top_logprobs": {}, "stream_options": {}, "seed": {}, "stop": {}, "presence_penalty": {},
		"frequency_penalty": {}, "thinking": {},
	}
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			return invalidResponses("invalid_parameter", name, "Unknown Responses parameter.")
		}
	}
	checks := []topLevelCheck{
		{reject: rejectStoreTrue},
		{reject: rejectBackgroundAsync},
		{reject: rejectPresentUserState},
		{reject: rejectIncludeHosted},
		{reject: rejectServiceTier},
		{reject: rejectTruncation},
	}
	for _, check := range checks {
		if err := check.reject(fields); err != nil {
			return err
		}
	}
	return nil
}

func rejectStoreTrue(fields map[string]json.RawMessage) error {
	raw, ok := fields["store"]
	if !ok || isJSONNull(raw) {
		return nil
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return invalidResponses("invalid_parameter", "store", "The store parameter must be a boolean.")
	}
	if value {
		return unsupportedResponses("store", "Stored responses are not supported.")
	}
	return nil
}

func rejectPresentUserState(fields map[string]json.RawMessage) error {
	unsupported := map[string]string{
		"previous_response_id":   "Stateful response recovery is not supported.",
		"conversation":           "Stateful response recovery is not supported.",
		"metadata":               "Response metadata persistence is not supported.",
		"prompt":                 "Prompt templates are not supported.",
		"prompt_cache_key":       "Prompt cache keys are not supported.",
		"prompt_cache_options":   "Prompt cache options are not supported.",
		"prompt_cache_retention": "Prompt cache retention is not supported.",
		"context_management":     "Context management is not supported.",
		"include":                "Hosted tool inclusion is not supported.",
		"max_tool_calls":         "Maximum tool call limits are not supported.",
		"moderation":             "Moderation controls are not supported.",
		"safety_identifier":      "Safety identifiers are not supported.",
		"top_logprobs":           "Top logprobs are not supported.",
	}
	for name, message := range unsupported {
		if raw, ok := fields[name]; ok && !isJSONNull(raw) {
			return unsupportedResponses(name, message)
		}
	}
	return nil
}

func rejectBackgroundAsync(fields map[string]json.RawMessage) error {
	raw, ok := fields["background"]
	if !ok || isJSONNull(raw) {
		return nil
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return invalidResponses("invalid_parameter", "background", "The background parameter must be a boolean.")
	}
	if value {
		return unsupportedResponses("background", "Background responses are not supported.")
	}
	return nil
}

func rejectIncludeHosted(fields map[string]json.RawMessage) error {
	if raw, ok := fields["include"]; ok && !isJSONNull(raw) {
		return unsupportedResponses("include", "Hosted tool inclusion is not supported.")
	}
	return nil
}

func rejectHostedTools(fields map[string]json.RawMessage) error {
	raw, ok := fields["tools"]
	if !ok {
		return nil
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) == nil {
		for _, tool := range tools {
			rawType, hasType := tool["type"]
			if !hasType {
				continue
			}
			var toolType string
			if json.Unmarshal(rawType, &toolType) == nil && toolType != "function" {
				return unsupportedResponses("tools", "Only function tools are supported.")
			}
		}
	}
	return nil
}

func rejectServiceTier(fields map[string]json.RawMessage) error {
	raw, ok := fields["service_tier"]
	if !ok || isJSONNull(raw) {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return invalidResponses("invalid_parameter", "service_tier", "The service_tier parameter must be a string or null.")
	}
	if value != "auto" {
		return unsupportedResponses("service_tier", "Only service_tier=auto is supported.")
	}
	return nil
}

func rejectTruncation(fields map[string]json.RawMessage) error {
	raw, ok := fields["truncation"]
	if !ok || isJSONNull(raw) {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return invalidResponses("invalid_parameter", "truncation", "The truncation parameter must be a string or null.")
	}
	if value != "disabled" {
		return unsupportedResponses("truncation", "Only truncation=disabled is supported.")
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}
