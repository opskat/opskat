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

	if ext.Manifest.Policies.Type != "" {
		if assetID <= 0 {
			return "", fmt.Errorf("ext_exec: %s.%s requires asset (extension declares policy type %q)",
				extName, toolName, ext.Manifest.Policies.Type)
		}
		action, _, err := ext.Plugin.CheckPolicy(ctx, toolName, argsJSON)
		if err == nil && action != "" {
			policyGroups := execToolExecutor.GetExtensionPolicyGroups(
				extName, ext.Manifest.Policies.Type, assetID,
			)
			result := policy.CheckExtensionPolicy(ctx, policyGroups, action)
			switch result.Decision {
			case aictx.Deny:
				return "", fmt.Errorf("ext_exec: policy denied: %s", result.Message)
			case aictx.NeedConfirm:
				// 这里不接受 permission.WithPreapproved 那条豁免：扩展策略的确认没有
				// opsctl 侧的等价物（requireApproval 查的是内置类型的策略 / Grant，
				// 不认识扩展 manifest 里的 action），所以"没有 checker"在这条路径上
				// 一定是接线漏了。从前它 checker == nil 时直接往下执行，等于把一条
				// 需要用户点头的扩展调用静默放行。
				checker, err := permission.RequireChecker(ctx)
				if err != nil {
					return "", fmt.Errorf("ext_exec: %s.%s needs confirmation but %w", extName, toolName, err)
				}
				confirmResult := checker.HandleConfirm(ctx, assetID, ext.Manifest.Policies.Type, extName+"."+toolName)
				if confirmResult.Decision != aictx.Allow {
					return "", fmt.Errorf("ext_exec: user denied: %s.%s", extName, toolName)
				}
			}
		}
	}

	result, err := ext.Plugin.CallTool(ctx, toolName, argsJSON)
	if err != nil {
		return "", fmt.Errorf("ext_exec: %s.%s failed: %w", extName, toolName, err)
	}

	return string(result), nil
}
