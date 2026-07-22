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

// canonicalizingTools 记录哪些工具的 args["command"] 是**目标资产类型自己的** exec DSL，
// 因此在写审计前应该按 permission.CanonicalizeFor(asset.Type) 规范化——与审批弹窗、策略
// 检查看到的是同一个串。
//
// 目前只有 "exec" 注册（extractor_default.go）。ext_exec 的 args 形状恰好也是
// asset+command（Task 9 把 exec_tool 改名而来），但它的 command 是扩展自己的调用语法
// （`<extension> <tool> --flag=value`），从来不是资产类型的 exec DSL——runner.
// resolveAssetForAudit 曾经只看参数形状、不看工具名，把这条命令也喂给
// permission.CanonicalizeFor(asset.Type)：对 k8s 资产，BuildK8sCommandPlan 不会因为
// 语法不认识而报错（cmdline.Words 只是分词），而是把整句话当成 kubectl 参数，注入
// --context/--namespace，写出一条从未执行、也从未被批准过的审计命令。
//
// 与 RegisterGroupScopedTool 同一种"注册而不是分支"的解法：resolveAssetForAudit 只查表，
// 不按工具名 if/switch。
var (
	canonicalizingMu    sync.RWMutex
	canonicalizingTools = map[string]bool{}
)

// RegisterCanonicalizingTool 把 toolName 标记为"command 参数是资产类型自己的 exec DSL"，
// 使其在写审计前经过 permission.CanonicalizeFor(asset.Type) 规范化。只有真正把
// args["command"] 当作目标资产类型 exec 语法使用的工具才应该注册——见本文件上方
// canonicalizingTools 的文档注释。
func RegisterCanonicalizingTool(toolName string) {
	canonicalizingMu.Lock()
	defer canonicalizingMu.Unlock()
	canonicalizingTools[toolName] = true
}

// ShouldCanonicalizeCommand 报告 toolName 是否通过 RegisterCanonicalizingTool 注册过。
func ShouldCanonicalizeCommand(toolName string) bool {
	canonicalizingMu.RLock()
	defer canonicalizingMu.RUnlock()
	return canonicalizingTools[toolName]
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
