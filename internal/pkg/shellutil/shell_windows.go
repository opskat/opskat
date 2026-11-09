//go:build windows

// Package shellutil 提供跨子系统共用的"系统默认 shell"选择：本地终端资产
// （internal/service/localterm_svc）用它启动交互式会话，通用资产的本地命令执行方式
// （命令模板为空时，见 docs/specs/2026-09-28-generic-asset.md「本地命令」）用它执行整条
// shell 命令——两处必须选出同一个 shell，因此只在这里实现一次。
package shellutil

import (
	"os"
	"os/exec"
)

// DefaultShell 按 pwsh → powershell → cmd 兜底。
func DefaultShell() string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if c := os.Getenv("COMSPEC"); c != "" {
		return c
	}
	return "cmd.exe"
}
