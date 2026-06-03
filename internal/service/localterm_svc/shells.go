package localterm_svc

import "strings"

// ShellInfo 描述一个可供用户选择的本地 shell 预设。
// Args 让 "shell+固定参数" 的预设(如 WSL 发行版、Git Bash 登录)可一键选中。
type ShellInfo struct {
	Name string   `json:"name"`           // 展示名,如 "zsh" / "WSL: Ubuntu" / "Git Bash"
	Path string   `json:"path"`           // 可执行文件路径
	Args []string `json:"args,omitempty"` // 启动参数,如 ["-d","Ubuntu"]
}

// parseWSLOutput 解析 `wsl -l -q` 的输出。wsl 输出是 UTF-16LE,
// v1 用"去 NUL + 去 CR + 按行切"解析(ASCII 名字够用;非 ASCII 后续再上正规 UTF-16 解码)。
func parseWSLOutput(raw []byte) []string {
	cleaned := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b != 0x00 && b != '\r' {
			cleaned = append(cleaned, b)
		}
	}
	var distros []string
	for _, line := range strings.Split(string(cleaned), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			distros = append(distros, line)
		}
	}
	return distros
}
