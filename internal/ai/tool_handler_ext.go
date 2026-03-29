package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/pkg/extension"
)

// ExtensionToolExecutor provides extension tool execution to the AI system.
type ExtensionToolExecutor interface {
	FindExtensionByTool(extName, toolName string) *extension.Extension
}

var execToolExecutor ExtensionToolExecutor

// SetExecToolExecutor wires the extension executor into the exec_tool handler.
func SetExecToolExecutor(executor ExtensionToolExecutor) {
	execToolExecutor = executor
}

func handleExecTool(ctx context.Context, args map[string]any) (string, error) {
	extName := argString(args, "extension")
	if extName == "" {
		return "", fmt.Errorf("exec_tool: extension name is required")
	}
	toolName := argString(args, "tool")
	if toolName == "" {
		return "", fmt.Errorf("exec_tool: tool name is required")
	}

	if execToolExecutor == nil {
		return "", fmt.Errorf("exec_tool: extension %q not found (no extensions loaded)", extName)
	}

	ext := execToolExecutor.FindExtensionByTool(extName, toolName)
	if ext == nil {
		return "", fmt.Errorf("exec_tool: tool %q not found in extension %q", toolName, extName)
	}

	toolArgs, _ := args["args"].(map[string]any)
	argsJSON, err := json.Marshal(toolArgs)
	if err != nil {
		return "", fmt.Errorf("exec_tool: marshal args: %w", err)
	}

	result, err := ext.Plugin.CallTool(ctx, toolName, argsJSON)
	if err != nil {
		return "", fmt.Errorf("exec_tool: %s.%s failed: %w", extName, toolName, err)
	}

	return string(result), nil
}
