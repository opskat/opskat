package localterm_svc

// ShellInfo 描述一个可供用户选择的本地 shell 预设。
// Args 让 "shell+固定参数" 的预设(如 WSL 发行版、Git Bash 登录)可一键选中。
type ShellInfo struct {
	Name string   `json:"name"`           // 展示名,如 "zsh" / "WSL: Ubuntu" / "Git Bash"
	Path string   `json:"path"`           // 可执行文件路径
	Args []string `json:"args,omitempty"` // 启动参数,如 ["-d","Ubuntu"]
}
