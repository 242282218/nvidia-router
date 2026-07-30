package responses

import (
	"encoding/json"

	"nvidia-router/internal/apierror"
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

type topLevelCheck struct {
	reject func(map[string]json.RawMessage) error
}

func rejectUnsupportedTopLevel(fields map[string]json.RawMessage) error {
	checks := []topLevelCheck{
		{reject: rejectStoreTrue},
		{reject: rejectBackgroundAsync},
		{reject: rejectPresentUserState},
		{reject: rejectIncludeHosted},
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
	if !ok {
		return nil
	}
	var value *bool
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return invalidResponses("invalid_parameter", "store", "The store parameter must be a boolean.")
	}
	if *value {
		return unsupportedResponses("store", "Stored responses are not supported.")
	}
	return nil
}

func rejectPresentUserState(fields map[string]json.RawMessage) error {
	if _, ok := fields["previous_response_id"]; ok {
		return unsupportedResponses("previous_response_id", "Stateful response recovery is not supported.")
	}
	if _, ok := fields["conversation"]; ok {
		return unsupportedResponses("conversation", "Stateful response recovery is not supported.")
	}
	if _, ok := fields["metadata"]; ok {
		return unsupportedResponses("metadata", "Response metadata persistence is not supported.")
	}
	if _, ok := fields["prompt_cache_key"]; ok {
		return unsupportedResponses("prompt_cache_key", "Prompt cache keys are not supported.")
	}
	return nil
}

func rejectBackgroundAsync(fields map[string]json.RawMessage) error {
	raw, ok := fields["background"]
	if !ok {
		return nil
	}
	var value *bool
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return invalidResponses("invalid_parameter", "background", "The background parameter must be a boolean.")
	}
	if *value {
		return unsupportedResponses("background", "Background responses are not supported.")
	}
	return nil
}

func rejectIncludeHosted(fields map[string]json.RawMessage) error {
	if _, ok := fields["include"]; ok {
		return unsupportedResponses("include", "Hosted tool inclusion is not supported.")
	}
	return nil
}

func rejectHostedTools(fields map[string]json.RawMessage) error {
	raw, ok := fields["tools"]
	if !ok {
		return nil
	}
	var tools []struct {
		Type string          `json:"type"`
		Name json.RawMessage `json:"name"`
	}
	if json.Unmarshal(raw, &tools) == nil {
		for _, tool := range tools {
			if tool.Type != "function" {
				return unsupportedResponses("tools", "Only function tools are supported.")
			}
		}
	}
	return nil
}
