package audit

import (
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// 注册非协议特有的常见工具提取器；协议特有(exec_k8s)在各自子包 init() 注册。
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
	RegisterExtractor("exec_etcd", func(a map[string]any) string {
		// 手写的第二份 format，与 etcd_svc.FormatCommand（helper 走的那份）已经分叉，
		// 不再等价：这里 key/value 从不加引号，
		// 也从不输出 --limit=/--revision=/--lease=/--ttl=。audit 不便引 helper
		// （避免循环）是当初手抄的理由，但现在不要为了"追平"去改这段逻辑——
		// 这整块连同 exec_etcd 工具本身会在 Task 10（14 个旧工具下线）一并删除。
		// prefix 仍兼容 bool 与字符串 "true"，避免 LLM 传字符串时审计日志丢失 --prefix。
		op := strings.ReplaceAll(aictx.ArgString(a, "op"), "_", " ")
		parts := []string{op}
		if k := aictx.ArgString(a, "key"); k != "" {
			parts = append(parts, k)
		}
		if v := aictx.ArgString(a, "value"); v != "" {
			parts = append(parts, v)
		}
		if aictx.ArgBool(a, "prefix") {
			parts = append(parts, "--prefix")
		}
		return strings.Join(parts, " ")
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
