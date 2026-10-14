package audit

import "sync"

// resultProjectors 按工具名登记的审计 Result 投影：WriteToolCall 落库前把
// info.Result 替换成投影结果，而不是原样存下工具的返回值。
//
// 存在的意义与 [RegisterGroupScopedTool] 同一种"注册而不是按名字分支"（AGENTS.md）：
// get_asset_secret 的 info.Result 在允许放行时就是被取出的密钥明文本身
// （docs/specs/2026-09-28-generic-asset.md「取值」："审计：只记录资产、字段名和判定结果，
// 不记录值"）——这是唯一一个"返回值本身就是不能落审计的敏感数据"的工具，其余工具的
// Result 是命令输出/摘要，Audit 的 raw-by-default 契约（见 audit_test.go 的
// TestWriteToolCall_PreservesAllColumnsVerbatimIncludingLiteralRedacted）继续原样保留：
// 没注册投影的工具完全不受影响。
//
// 投影函数只拿得到 ToolCallInfo（含 ArgsJSON / Decision / Error），拿不到、也不应该
// 依赖 info.Result 本身的内容——那正是要被替换掉的字段；否则将来给它换一条不同措辞的
// "已拒绝"文案，都可能不小心把值原文抄一遍进投影结果里。
var (
	resultProjectorsMu sync.RWMutex
	resultProjectors   = map[string]func(ToolCallInfo) string{}
)

// RegisterResultProjector 为 toolName 注册一个审计 Result 投影函数。重复/空注册 panic，
// 与本包其余注册表（RegisterExtractor 等）同一原则：注册冲突是启动期的编程错误。
func RegisterResultProjector(toolName string, fn func(ToolCallInfo) string) {
	if toolName == "" || fn == nil {
		panic("audit: invalid result projector registration")
	}
	resultProjectorsMu.Lock()
	defer resultProjectorsMu.Unlock()
	if _, exists := resultProjectors[toolName]; exists {
		panic("audit: duplicate result projector registration " + toolName)
	}
	resultProjectors[toolName] = fn
}

// projectResult 返回该工具注册的 Result 投影（如有），否则原样返回 info.Result。
func projectResult(info ToolCallInfo) string {
	resultProjectorsMu.RLock()
	fn, ok := resultProjectors[info.ToolName]
	resultProjectorsMu.RUnlock()
	if !ok {
		return info.Result
	}
	return fn(info)
}
