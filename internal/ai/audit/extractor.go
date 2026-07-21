package audit

import "sync"

// CommandExtractorFunc 从工具参数 map 抽出命令摘要，供审计日志展示。
type CommandExtractorFunc func(args map[string]any) string

var (
	extractorsMu   sync.RWMutex
	extractors     = map[string]CommandExtractorFunc{}
	auditToolNames = map[string]string{}
)

// RegisterExtractor 注册工具名 → 命令摘要提取器。
// 通常各协议子包在 init() 中调用本函数；同名重复注册以最后一次为准。
func RegisterExtractor(toolName string, fn CommandExtractorFunc) {
	extractorsMu.Lock()
	defer extractorsMu.Unlock()
	extractors[toolName] = fn
}

// RegisterToolAlias 注册执行工具名到持久化审计工具名的映射。
// 它只改变 audit_logs.tool_name，不改变实际工具 dispatch 名或 provider schema。
func RegisterToolAlias(toolName, alias string) {
	extractorsMu.Lock()
	defer extractorsMu.Unlock()
	auditToolNames[toolName] = alias
}

// ToolNameForAudit 返回工具在持久化审计中的分类名，未注册别名时保持原名。
func ToolNameForAudit(toolName string) string {
	extractorsMu.RLock()
	alias, ok := auditToolNames[toolName]
	extractorsMu.RUnlock()
	if ok {
		return alias
	}
	return toolName
}

// ExtractCommandForAudit 调用已注册的提取器返回命令摘要，未注册返回空串。
// opsctl 兼容："exec" 工具名被规整为 "run_command"。
func ExtractCommandForAudit(toolName string, args map[string]any) string {
	if toolName == "exec" {
		toolName = "run_command"
	}
	extractorsMu.RLock()
	fn, ok := extractors[toolName]
	extractorsMu.RUnlock()
	if ok {
		return fn(args)
	}
	return ""
}
