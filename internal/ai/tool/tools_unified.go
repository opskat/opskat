package tool

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"

	"github.com/opskat/opskat/internal/ai/permission"
)

// unifiedTools exec 按资产真实类型（见 permission.RegisteredExecTypes，注册发生在
// internal/ai/execimpl 的 init）派发命令，取代按类型定义的专用工具；help 返回该类型的用法
// 文档并把它标记为"该会话已知晓"。两者共用 assetref 解析同一个 asset 参数（数字 id
// 或名称）。exec 标 Serial（跟原 run_command/exec_sql 等一致：远端连接复用、审批弹窗、
// 审计排序都要求整轮内串行）；help 不做远程调用，不标 Serial。
func unifiedTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "exec",
			// 支持的类型列表从执行器注册表生成，不手写：手写那份曾经停在
			// "(ssh, serial, database, redis, k8s)"，而 mongodb/etcd/kafka 接入之后
			// 没人想起来改它——模型读到的工具描述里，那三种类型压根不存在。
			// 这份描述随每个工具列表下发，是模型判断"exec 管不管这个类型"的第一手依据。
			// tool_registry.go 已 blank-import execimpl，所以 Tools() 跑到这里时注册表是全的。
			DescStr: "Execute a command against an asset. Dispatches by the asset's real type " +
				"(" + strings.Join(permission.RegisteredExecTypes(), ", ") + ") — you don't need to know the type ahead of time. " +
				"The first time you use exec against a given asset type in a conversation, call help first " +
				"to learn its command syntax; exec will tell you to if you skip that step. " +
				"Credentials are resolved automatically from the app's encrypted store — do not ask the user for passwords.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset":   {Type: "string", Description: "Target asset id or name. Use list_assets to find it."},
					"command": {Type: "string", Description: "Command to run, in the syntax for this asset's type — call help first if you don't know it."},
					"scope":   {Type: "string", Description: "Optional connection-level target that is not part of the command itself: database name for database assets, db index for redis assets. Ignored for other types."},
				},
				Required: []string{"asset", "command"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleExec(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "help",
			DescStr: "Get the command syntax and usage notes for an asset's type, so you can call exec correctly. " +
				"Call this the first time you use exec against a given asset type in a conversation.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset": {Type: "string", Description: "Target asset id or name whose type's usage you want to learn. Use list_assets to find it."},
				},
				Required: []string{"asset"},
			},
			IsSerial: false,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleHelp(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
