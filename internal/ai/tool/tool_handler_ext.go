package tool

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/pkg/extension"
)

// ExtensionToolExecutor provides extension tool execution to the AI system.
type ExtensionToolExecutor interface {
	FindExtensionByTool(extName, toolName string) *extension.Extension
	FindToolDef(extName, toolName string) (extension.ToolDef, bool)
	GetExtensionPolicyGroups(extName, assetType string, assetID int64) []string
}

var execToolExecutor ExtensionToolExecutor

// SetExecToolExecutor wires the extension executor into the ext_exec handler.
func SetExecToolExecutor(executor ExtensionToolExecutor) {
	execToolExecutor = executor
}

func handleExecTool(ctx context.Context, args map[string]any) (string, error) {
	command := aictx.ArgString(args, "command")
	if command == "" {
		return "", fmt.Errorf("ext_exec: command is required")
	}

	if execToolExecutor == nil {
		return "", fmt.Errorf("ext_exec: command %q not found (no extensions loaded)", command)
	}

	// 此时还不知道 ToolDef——parseExtCommand 需要它按声明类型转换 flag——所以先只用
	// cmdline 切出扩展名与工具名去定位 ToolDef，再用 parseExtCommand 对同一个 command
	// 做一次完整解析。
	c, err := cmdline.Parse(command)
	if err != nil {
		return "", fmt.Errorf("ext_exec: %w", err)
	}
	if len(c.Args) == 0 {
		return "", fmt.Errorf("ext_exec: command %q names an extension but no tool; use `<extension> <tool> [--flags]`", command)
	}
	extName, toolName := c.Verb, c.Args[0]

	ext := execToolExecutor.FindExtensionByTool(extName, toolName)
	if ext == nil {
		return "", fmt.Errorf("ext_exec: tool %q not found in extension %q", toolName, extName)
	}
	def, ok := execToolExecutor.FindToolDef(extName, toolName)
	if !ok {
		return "", fmt.Errorf("ext_exec: tool %q not found in extension %q", toolName, extName)
	}

	_, _, argsJSON, err := parseExtCommand(command, def)
	if err != nil {
		return "", err
	}

	// asset 是可选的：非资产范围的扩展不该被迫编一个资产。assetref 是仓内唯一的资产
	// 标识契约（数字 id 或名称），不在这里另接一个数字 id 通道。
	var assetID int64
	if assetRef := aictx.ArgString(args, "asset"); assetRef != "" {
		asset, err := assetref.Resolve(ctx, assetRef)
		if err != nil {
			return "", fmt.Errorf("ext_exec: %w", err)
		}
		assetID = asset.ID
	}

	result, err := ExecuteExtensionTool(ctx, execToolExecutor, assetID, extName, toolName, argsJSON)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// ExecuteExtensionTool is the single policy-and-execution seam shared by AI ext_exec
// and the desktop-delegated opsctl ext exec path.
func ExecuteExtensionTool(ctx context.Context, executor ExtensionToolExecutor, assetID int64,
	extName, toolName string, argsJSON []byte) ([]byte, error) {
	ext := executor.FindExtensionByTool(extName, toolName)
	if ext == nil || ext.Plugin == nil {
		return nil, fmt.Errorf("ext_exec: tool %q not found in extension %q", toolName, extName)
	}
	policyType := ext.Manifest.Policies.Type
	if policyType == "" {
		if err := confirmExtensionTool(ctx, assetID, extName, toolName, "extension declares no policy type"); err != nil {
			return nil, err
		}
	} else {
		if assetID <= 0 {
			return nil, fmt.Errorf("ext_exec: %s.%s requires asset (extension declares policy type %q)",
				extName, toolName, policyType)
		}
		action, _, err := ext.Plugin.CheckPolicy(ctx, toolName, argsJSON)
		if err != nil {
			return nil, fmt.Errorf("ext_exec: %s.%s policy check failed: %w", extName, toolName, err)
		}
		if action == "" {
			if err := confirmExtensionTool(ctx, assetID, extName, toolName, "extension policy returned no action"); err != nil {
				return nil, err
			}
		} else {
			groups := executor.GetExtensionPolicyGroups(extName, policyType, assetID)
			result := policy.CheckExtensionPolicy(ctx, groups, action)
			aictx.RecordDecision(ctx, result)
			switch result.Decision {
			case aictx.Deny:
				return nil, fmt.Errorf("ext_exec: policy denied: %s", result.Message)
			case aictx.NeedConfirm:
				if err := confirmExtensionTool(ctx, assetID, extName, toolName, policyType); err != nil {
					return nil, err
				}
			}
		}
	}
	result, err := ext.Plugin.CallTool(ctx, toolName, argsJSON)
	if err != nil {
		return nil, fmt.Errorf("ext_exec: %s.%s failed: %w", extName, toolName, err)
	}
	return result, nil
}

func confirmExtensionTool(ctx context.Context, assetID int64, extName, toolName, reason string) error {
	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return fmt.Errorf("ext_exec: %s.%s needs confirmation (%s) but %w", extName, toolName, reason, err)
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return fmt.Errorf("ext_exec: %s.%s needs confirmation (%s) but no confirmation callback is configured",
			extName, toolName, reason)
	}
	resp := confirm(ctx, "single", []permission.ApprovalItem{{
		Type:    "ext_tool",
		AssetID: assetID,
		Command: extName + "." + toolName,
		Detail:  reason,
	}})
	if resp.Decision != "allow" {
		aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny})
		return fmt.Errorf("ext_exec: user denied: %s.%s", extName, toolName)
	}
	aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow})
	return nil
}
