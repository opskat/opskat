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
// 关于 "exec"：它有自己的注册（见 extractor_default.go），不借用别的工具的提取器。
// 两条路径会走到这里——
//   - opsctl CLI 的 `opsctl exec`（cmd/opsctl/command/exec.go）直接调 WriteToolCall，
//     不经过 runner 的 auditMiddleware，命令摘要永远由本函数决定；
//   - 统一 AI exec 工具（internal/ai/tool/tools_unified.go）正常情况下**不**依赖本函数：
//     auditMiddleware 会在工具执行前解析资产、按需算出规范化命令（k8s 注入
//     --context/--namespace；etcd/mongo/kafka 走各自 DSL 的 round trip，规范化大小写、
//     复合命令拼写与 flag 顺序），通过 ToolCallInfo.Command 直接覆盖（见
//     runner.resolveAssetForAudit）。只有资产解析不出来（引用不存在/歧义）时才落回这里，
//     读 args["command"]——这对 ssh/serial/redis/database 同样正确，因为这些类型没有
//     规范化钩子，raw 命令本来就等于 effective 命令。
func ExtractCommandForAudit(toolName string, args map[string]any) string {
	extractorsMu.RLock()
	fn, ok := extractors[toolName]
	extractorsMu.RUnlock()
	if ok {
		return fn(args)
	}
	return ""
}

// unregisterExtractorForTest 摘掉一个已注册的提取器并返回还原函数。仅供测试：
// 用来验证某个工具的提取不依赖另一个工具的注册是否还在。
func unregisterExtractorForTest(toolName string) func() {
	extractorsMu.Lock()
	old, existed := extractors[toolName]
	delete(extractors, toolName)
	extractorsMu.Unlock()
	return func() {
		extractorsMu.Lock()
		defer extractorsMu.Unlock()
		if existed {
			extractors[toolName] = old
			return
		}
		delete(extractors, toolName)
	}
}
