package opskat

import (
	"encoding/json"
	"fmt"
)

// dispatch routes a function call to the registered handler.
func dispatch(fnName string, input []byte) (json.RawMessage, error) {
	switch fnName {
	case "describe":
		return dispatchDescribe()
	case "execute_tool":
		return dispatchTool(input)
	case "execute_action":
		return dispatchAction(input)
	case "check_policy":
		return dispatchPolicy(input)
	case "validate_config":
		return dispatchConfigValidator(input)
	default:
		return nil, fmt.Errorf("unknown function: %s", fnName)
	}
}

// toolCall is the shape of both execute_tool and check_policy input: the host
// asks the same question about the same call, once to classify it and once to run it.
type toolCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

func parseToolCall(input []byte) (*toolEntry, toolCall, error) {
	var req toolCall
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, req, fmt.Errorf("parse tool request: %w", err)
	}
	entry, ok := tools[req.Tool]
	if !ok {
		return nil, req, fmt.Errorf("unknown tool: %s", req.Tool)
	}
	return entry, req, nil
}

func dispatchTool(input []byte) (json.RawMessage, error) {
	entry, req, err := parseToolCall(input)
	if err != nil {
		return nil, err
	}
	result, err := entry.invoke(&ToolContext{Tool: req.Tool, Args: req.Args})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func dispatchAction(input []byte) (json.RawMessage, error) {
	var req struct {
		Action string          `json:"action"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("parse action request: %w", err)
	}
	handler, ok := actions[req.Action]
	if !ok {
		return nil, fmt.Errorf("unknown action: %s", req.Action)
	}
	result, err := handler(&ActionContext{
		Action: req.Action,
		Args:   req.Args,
		Events: newEventWriter(),
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// dispatchPolicy answers from the tool's own registration. The action a tool
// requests is part of declaring the tool, so there is no per-tool switch here to
// fall out of step with the handler table.
func dispatchPolicy(input []byte) (json.RawMessage, error) {
	entry, req, err := parseToolCall(input)
	if err != nil {
		return nil, err
	}
	resource := ""
	if entry.resource != nil {
		resource = entry.resource(req.Args)
	}
	return json.Marshal(map[string]string{
		"action":   entry.action,
		"resource": resource,
	})
}

func dispatchConfigValidator(input []byte) (json.RawMessage, error) {
	if configValidator == nil {
		return json.Marshal([]ValidationError{})
	}
	errors := configValidator(input)
	if errors == nil {
		errors = []ValidationError{}
	}
	return json.Marshal(errors)
}
