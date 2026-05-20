package tool

import (
	"github.com/cago-frame/agents/tool"
)

// Tools 返回所有 cago 原生工具实例。各 *_tools_*.go 文件直接以 *tool.RawTool 字面量定义工具。
// handler 内部按 cago 原生 (ctx, in) → (*ToolResultBlock, error) 签名构造返回值，
// 权限决策通过 setCheckResult 透出，审计写入由 auditMiddleware 异步完成。
func Tools() []tool.Tool {
	tools := make([]tool.Tool, 0, 24)
	tools = append(tools, assetTools()...)
	tools = append(tools, execTools()...)
	tools = append(tools, dataTools()...)
	tools = append(tools, kafkaTools()...)
	tools = append(tools, extTools()...)
	return tools
}
