package compat

import (
	"encoding/json"
	"fmt"
)

const MaxToolArgumentsBytes = 4 << 20

type ToolCall struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type toolCallState struct {
	ToolCall
	arguments []byte
}

type ToolCallAccumulator struct {
	calls map[int]*toolCallState
	total int
}

func (a *ToolCallAccumulator) Add(delta ToolCallDelta) error {
	if delta.Index < 0 {
		return fmt.Errorf("tool call index must be non-negative")
	}
	if a.calls == nil {
		a.calls = make(map[int]*toolCallState)
	}
	call := a.calls[delta.Index]
	if call == nil {
		call = &toolCallState{ToolCall: ToolCall{Index: delta.Index}}
		a.calls[delta.Index] = call
	}
	if delta.ID != "" {
		if call.ID != "" && call.ID != delta.ID {
			return fmt.Errorf("tool call %d has conflicting IDs", delta.Index)
		}
		call.ID = delta.ID
	}
	if delta.Name != "" {
		if call.Name != "" && call.Name != delta.Name {
			return fmt.Errorf("tool call %d has conflicting names", delta.Index)
		}
		call.Name = delta.Name
	}
	if len(delta.Arguments) > 0 {
		if a.total+len(delta.Arguments) > MaxToolArgumentsBytes {
			return fmt.Errorf("tool call arguments exceed %d bytes", MaxToolArgumentsBytes)
		}
		call.arguments = append(call.arguments, delta.Arguments...)
		a.total += len(delta.Arguments)
	}
	return nil
}

func (a *ToolCallAccumulator) Calls() ([]ToolCall, error) {
	if len(a.calls) == 0 {
		return nil, nil
	}
	indexes := sortedToolIndexes(a.calls)
	result := make([]ToolCall, 0, len(indexes))
	for _, index := range indexes {
		call := a.calls[index]
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", index)
		}
		name := call.Name
		if name == "" {
			return nil, fmt.Errorf("tool call %d is missing a function name", index)
		}
		arguments := string(call.arguments)
		if arguments == "" {
			arguments = "{}"
		}
		if !json.Valid([]byte(arguments)) {
			return nil, fmt.Errorf("tool call %d has malformed arguments", index)
		}
		result = append(result, ToolCall{Index: index, ID: id, Name: name, Arguments: arguments})
	}
	return result, nil
}
