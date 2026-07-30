package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
)

var validReasoningEfforts = map[string]struct{}{
	"none": {}, "minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {},
}

func rejectUnsupported(fields map[string]json.RawMessage) error {
	raw, ok := fields["store"]
	if !ok {
		return nil
	}
	var store *bool
	if json.Unmarshal(raw, &store) != nil || store == nil {
		return invalidRequest("invalid_parameter", "store", "The store parameter must be a boolean.")
	}
	if *store {
		return invalidRequest("unsupported_parameter", "store", "Stored responses are not supported.")
	}
	return nil
}

func validateModel(request Request, model modelcatalog.Model) error {
	if model.PublicID != request.publicModel || model.UpstreamID == "" || model.Kind != modelcatalog.KindChat || !model.Enabled {
		return fmt.Errorf("prepare chat request: resolved model does not match request")
	}
	if request.requirements.Vision && !model.SupportsVision {
		return capabilityError("messages")
	}
	if request.requirements.Tools && !model.SupportsTools {
		return capabilityError("tools")
	}
	if request.requirements.Reasoning && (!model.SupportsReasoning || model.ReasoningWireFormat != "openai") {
		return capabilityError(reasoningParam(request.fields))
	}
	return nil
}

func capabilityError(param string) error {
	return invalidRequest("model_capability_unsupported", param, "The selected model does not support this request capability.")
}

func hasReasoning(fields map[string]json.RawMessage) bool {
	for _, name := range []string{"reasoning_effort", "reasoning", "thinking"} {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}

func reasoningParam(fields map[string]json.RawMessage) string {
	for _, name := range []string{"reasoning_effort", "reasoning", "thinking"} {
		if _, ok := fields[name]; ok {
			return name
		}
	}
	return "reasoning_effort"
}

func normalizeReasoning(fields map[string]json.RawMessage) error {
	if !hasReasoning(fields) {
		return nil
	}
	efforts := make([]string, 0, 3)
	for _, extractor := range []func(map[string]json.RawMessage) (string, bool, error){
		explicitReasoningEffort, reasoningObjectEffort, nativeThinkingEffort,
	} {
		effort, present, err := extractor(fields)
		if err != nil {
			return err
		}
		if present {
			efforts = append(efforts, effort)
		}
	}
	for _, effort := range efforts[1:] {
		if effort != efforts[0] {
			return invalidRequest("conflicting_reasoning_parameters", "reasoning_effort", "Reasoning parameters must not conflict.")
		}
	}
	delete(fields, "thinking")
	delete(fields, "reasoning")
	fields["reasoning_effort"], _ = json.Marshal(efforts[0])
	return nil
}

func explicitReasoningEffort(fields map[string]json.RawMessage) (string, bool, error) {
	raw, ok := fields["reasoning_effort"]
	if !ok {
		return "", false, nil
	}
	var effort string
	if json.Unmarshal(raw, &effort) != nil || !validReasoningEffort(effort) {
		return "", true, invalidRequest("invalid_parameter", "reasoning_effort", "The reasoning_effort value is not supported.")
	}
	return strings.ToLower(effort), true, nil
}

func reasoningObjectEffort(fields map[string]json.RawMessage) (string, bool, error) {
	raw, ok := fields["reasoning"]
	if !ok {
		return "", false, nil
	}
	var reasoning struct {
		Effort jsonString `json:"effort"`
	}
	if json.Unmarshal(raw, &reasoning) != nil || !reasoning.Effort.valid || !validReasoningEffort(reasoning.Effort.value) {
		return "", true, invalidRequest("invalid_parameter", "reasoning", "The reasoning parameter must include a supported effort.")
	}
	return strings.ToLower(reasoning.Effort.value), true, nil
}

func nativeThinkingEffort(fields map[string]json.RawMessage) (string, bool, error) {
	raw, ok := fields["thinking"]
	if !ok {
		return "", false, nil
	}
	var thinking struct {
		Type         jsonString `json:"type"`
		BudgetTokens *int64     `json:"budget_tokens"`
	}
	if json.Unmarshal(raw, &thinking) != nil || !thinking.Type.valid {
		return "", true, invalidRequest("invalid_parameter", "thinking", "The thinking parameter is invalid.")
	}
	switch thinking.Type.value {
	case "disabled":
		return "none", true, nil
	case "adaptive":
		return "medium", true, nil
	case "enabled":
		if thinking.BudgetTokens == nil {
			return "medium", true, nil
		}
		return effortForBudget(*thinking.BudgetTokens)
	default:
		return "", true, invalidRequest("invalid_parameter", "thinking", "The thinking type is not supported.")
	}
}

func effortForBudget(budget int64) (string, bool, error) {
	if budget <= 0 {
		return "", true, invalidRequest("invalid_parameter", "thinking", "The thinking budget must be positive.")
	}
	switch {
	case budget <= 768:
		return "minimal", true, nil
	case budget <= 4096:
		return "low", true, nil
	case budget <= 16384:
		return "medium", true, nil
	case budget <= 28672:
		return "high", true, nil
	default:
		return "xhigh", true, nil
	}
}

func validReasoningEffort(effort string) bool {
	_, ok := validReasoningEfforts[strings.ToLower(effort)]
	return ok
}

func invalidRequest(code, param, message string) *apierror.Error {
	var parameter *string
	if param != "" {
		parameter = &param
	}
	return &apierror.Error{
		Status: 400, Type: "invalid_request_error", Code: code, Message: message, Param: parameter,
	}
}
