package audit

import (
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
)

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
	RegisterExtractor("exec_etcd", func(a map[string]any) string {
		// 与 helper.FormatEtcdCommand 保持等价；audit 不便引 helper（避免循环），手写一份。
		// prefix 兼容 bool 与字符串 "true"，对齐 helper.argEtcdBool 的接受形态，
		// 否则 LLM 传字符串时审计日志会丢失 --prefix。
		op := strings.ReplaceAll(aictx.ArgString(a, "op"), "_", " ")
		parts := []string{op}
		if k := aictx.ArgString(a, "key"); k != "" {
			parts = append(parts, k)
		}
		if v := aictx.ArgString(a, "value"); v != "" {
			parts = append(parts, v)
		}
		if argBool(a, "prefix") {
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

// argBool 提取布尔参数,兼容 LLM 传入的 bool 与 "true" 字符串两种形态。
// 与 internal/ai/helper.argEtcdBool 保持一致(audit 不便反向引 helper 包)。
func argBool(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(strings.TrimSpace(b), "true")
	}
	return false
}
