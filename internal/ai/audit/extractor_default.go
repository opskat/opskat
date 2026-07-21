package audit

import (
	"github.com/opskat/opskat/internal/ai/aictx"
)

// 注册工具名 → 命令摘要提取器。每个工具各自注册，不互相借用：exec 曾经靠
// extractor.go 里一句 toolName 别名借用 run_command 的提取器，于是新工具的审计
// 取决于旧工具的注册是否还在——run_command 现已删除，别名与它一起没了，而 exec
// 的提取因为有自己的注册而毫发无损（TestExtractor_ExecSurvivesRunCommandRemoval）。
func init() {
	RegisterExtractor("exec", func(a map[string]any) string { return aictx.ArgString(a, "command") })
	RegisterExtractor("upload_file", func(a map[string]any) string {
		return "upload " + aictx.ArgString(a, "local_path") + " → " + aictx.ArgString(a, "remote_path")
	})
	RegisterExtractor("download_file", func(a map[string]any) string {
		return "download " + aictx.ArgString(a, "remote_path") + " → " + aictx.ArgString(a, "local_path")
	})
	RegisterExtractor("request_permission", func(a map[string]any) string {
		v := aictx.ArgString(a, "items")
		if reason := aictx.ArgString(a, "reason"); reason != "" {
			return "grant: " + v + " reason: " + reason
		}
		return "grant: " + v
	})
	RegisterExtractor("exec_tool", func(a map[string]any) string {
		return aictx.ArgString(a, "extension") + "." + aictx.ArgString(a, "tool")
	})
}
