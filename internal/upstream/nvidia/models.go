package nvidia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type modelRecord struct {
	ID string `json:"id"`
}

func parseModels(body io.Reader) ([]string, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, ErrProtocol
	}

	var records []modelRecord
	switch payload[0] {
	case '[':
		if err := json.Unmarshal(payload, &records); err != nil {
			return nil, fmt.Errorf("%w: decode model array", ErrProtocol)
		}
	case '{':
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("%w: decode model envelope", ErrProtocol)
		}
		var found bool
		for _, field := range []string{"data", "models", "results"} {
			value, ok := envelope[field]
			if !ok {
				continue
			}
			found = true
			if err := json.Unmarshal(value, &records); err != nil {
				return nil, fmt.Errorf("%w: decode %s models", ErrProtocol, field)
			}
			break
		}
		if !found {
			return nil, ErrProtocol
		}
	default:
		return nil, ErrProtocol
	}

	models := make(map[string]struct{}, len(records))
	for _, record := range records {
		modelID := strings.TrimSpace(record.ID)
		if modelID == "" {
			return nil, ErrProtocol
		}
		models[modelID] = struct{}{}
	}
	if len(models) == 0 {
		return nil, ErrProtocol
	}
	modelIDs := make([]string, 0, len(models))
	for modelID := range models {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	return modelIDs, nil
}
