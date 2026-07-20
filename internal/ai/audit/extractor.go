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
//
// "exec" 工具名被规整为 "run_command"：opsctl CLI 的 `opsctl exec`
// （cmd/opsctl/command/exec.go）直接调 WriteToolCall，不经过 runner 的
// auditMiddleware，审计命令摘要永远走这条别名。统一 AI exec 工具
// （internal/ai/tool/tools_unified.go）复用了同一个工具名 "exec"，但正常情况下
// 不会落到这条别名——auditMiddleware 会在工具执行前解析资产、按需算出规范化命令
// （目前只有 k8s，注入 --context/--namespace），通过 ToolCallInfo.Command 直接
// 覆盖（见 runner.resolveAssetForAudit），此函数根本不会被调用去决定它的展示值。
// 只有 auditMiddleware 解析不出资产（引用不存在/歧义）时才会落回这条别名，用
// run_command 提取器读 args["command"]——这对 ssh/serial/redis/database 同样正确，
// 因为这些类型没有规范化钩子，raw 命令本来就等于 effective 命令。
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
