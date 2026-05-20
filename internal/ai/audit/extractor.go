package audit

import "sync"

// CommandExtractorFunc 从工具参数 map 抽出命令摘要，供审计日志展示。
type CommandExtractorFunc func(args map[string]any) string

var (
	extractorsMu sync.RWMutex
	extractors   = map[string]CommandExtractorFunc{}
)

// RegisterExtractor 注册工具名 → 命令摘要提取器。
// 通常各协议子包在 init() 中调用本函数；同名重复注册以最后一次为准。
func RegisterExtractor(toolName string, fn CommandExtractorFunc) {
	extractorsMu.Lock()
	defer extractorsMu.Unlock()
	extractors[toolName] = fn
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
