package chat

import (
	"encoding/json"
	"fmt"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
)

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

	return nil
}

func hasReasoning(fields map[string]json.RawMessage) bool {
	for _, name := range []string{"reasoning_effort", "reasoning", "thinking"} {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
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
