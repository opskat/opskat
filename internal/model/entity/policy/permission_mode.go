package policy

import "fmt"

// 权限模式：命令没被规则放行、原本要问人时怎么处理。资产和分组上都可以设置。
const (
	// PermissionModeInherit 表示沿用上级分组的设置，都没设置时就是默认模式。
	PermissionModeInherit = ""
	// PermissionModeDefault 问人。
	PermissionModeDefault = "default"
	// PermissionModeAssisted 辅助审批：模型审核通过就自动执行，否则问人。
	PermissionModeAssisted = "assisted"
	// PermissionModeAutopilot 模型审核通过就自动执行，否则直接拒绝，不等人。
	PermissionModeAutopilot = "autopilot"
)

// ValidatePermissionMode 校验权限模式取值。
func ValidatePermissionMode(mode string) error {
	switch mode {
	case PermissionModeInherit, PermissionModeDefault, PermissionModeAssisted, PermissionModeAutopilot:
		return nil
	}
	return fmt.Errorf("无效的权限模式: %q", mode)
}
