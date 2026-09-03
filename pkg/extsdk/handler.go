package opskat

import (
	"encoding/json"
	"fmt"
)

// dispatch routes a function call to the registered handler.
func dispatch(fnName string, input []byte) (json.RawMessage, error) {
	switch fnName {
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

func dispatchTool(input []byte) (json.RawMessage, error) {
	var req struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("parse tool request: %w", err)
	}
	handler, ok := tools[req.Tool]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", req.Tool)
	}
	result, err := handler(&ToolContext{Tool: req.Tool, Args: req.Args})
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

func dispatchPolicy(input []byte) (json.RawMessage, error) {
	if policyChecker == nil {
		return nil, fmt.Errorf("no policy checker registered")
	}
	var req struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("parse policy request: %w", err)
	}
	action, resource := policyChecker(req.Tool, req.Args)
	return json.Marshal(map[string]string{
		"action":   action,
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
