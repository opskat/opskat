package audit

import "github.com/opskat/opskat/internal/ai/aictx"

// 注册非协议特有的常见工具提取器；协议特有(kafka_*, exec_k8s)在各自子包 init() 注册。
func init() {
	RegisterExtractor("run_command", func(a map[string]any) string { return aictx.ArgString(a, "command") })
	RegisterExtractor("upload_file", func(a map[string]any) string {
		return "upload " + aictx.ArgString(a, "local_path") + " → " + aictx.ArgString(a, "remote_path")
	})
	RegisterExtractor("download_file", func(a map[string]any) string {
		return "download " + aictx.ArgString(a, "remote_path") + " → " + aictx.ArgString(a, "local_path")
	})
	RegisterExtractor("exec_sql", func(a map[string]any) string { return aictx.ArgString(a, "sql") })
	RegisterExtractor("exec_redis", func(a map[string]any) string { return aictx.ArgString(a, "command") })
	RegisterExtractor("exec_mongo", func(a map[string]any) string { return aictx.ArgString(a, "operation") })
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
