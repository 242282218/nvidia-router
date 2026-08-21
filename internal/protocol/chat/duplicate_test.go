package chat

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func TestFastPathAvoidsForwardingDuplicateTopLevelKeys(t *testing.T) {
	// Duplicate "temperature" key at top level
	payload := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}],"temperature":0.5,"temperature":0.9}`)
	req, err := Parse(payload)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// The fields map should have one temperature (last-write-wins)
	var temp float64
	if err := json.Unmarshal(req.fields["temperature"], &temp); err != nil {
		t.Fatalf("unmarshal temperature: %v", err)
	}
	if temp != 0.9 {
		t.Errorf("temperature = %v; want 0.9 (last-write-wins)", temp)
	}

	// rawUnsafe should be true
	if !req.rawUnsafe {
		t.Errorf("rawUnsafe = false; want true when duplicate keys exist")
	}

	// MarshalFor should rebuild from fields (slow path), not return raw
	model := modelcatalog.Model{
		ID: 1, PublicID: "test", UpstreamID: "test", DisplayName: "Test",
		Kind: modelcatalog.KindChat, Enabled: true,
	}
	marshaled, err := req.MarshalFor(model)
	if err != nil {
		t.Fatalf("MarshalFor failed: %v", err)
	}

	// The marshaled output should not be the original raw bytes
	if string(marshaled) == string(payload) {
		t.Errorf("MarshalFor returned raw bytes despite duplicate keys")
	}

	// The marshaled output should have only one temperature
	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(marshaled, &unmarshaled); err != nil {
		t.Fatalf("unmarshal marshaled: %v", err)
	}
	if temp, ok := unmarshaled["temperature"].(float64); !ok || temp != 0.9 {
		t.Errorf("marshaled temperature = %v; want 0.9", temp)
	}
}

func TestHasDuplicateTopLevelKeysDetectsSimpleCases(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantDupe bool
	}{
		{
			name:     "no duplicates",
			payload:  `{"model":"test","temperature":0.5}`,
			wantDupe: false,
		},
		{
			name:     "duplicate temperature",
			payload:  `{"model":"test","temperature":0.5,"temperature":0.9}`,
			wantDupe: true,
		},
		{
			name:     "duplicate model",
			payload:  `{"model":"a","messages":[],"model":"b"}`,
			wantDupe: true,
		},
		{
			name:     "nested duplicate ignored",
			payload:  `{"model":"test","metadata":{"a":1,"a":2}}`,
			wantDupe: false,
		},
		{
			name:     "not an object",
			payload:  `[]`,
			wantDupe: false,
		},
		{
			name:     "malformed",
			payload:  `{invalid`,
			wantDupe: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := hasDuplicateTopLevelKeys([]byte(test.payload))
			if got != test.wantDupe {
				t.Errorf("hasDuplicateTopLevelKeys = %v; want %v", got, test.wantDupe)
			}
		})
	}
}

