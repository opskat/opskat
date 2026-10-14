//go:build !windows

// Package shellutil 提供跨子系统共用的"系统默认 shell"选择：本地终端资产
// （internal/service/localterm_svc）用它启动交互式会话，通用资产的本地命令执行方式
// （命令模板为空时，见 docs/specs/2026-09-28-generic-asset.md「本地命令」）用它执行整条
// shell 命令——两处必须选出同一个 shell，因此只在这里实现一次。
package shellutil

import "os"

// DefaultShell 返回当前用户的默认 shell：SHELL 环境变量非空时用它，否则回落到 /bin/sh。
func DefaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}
