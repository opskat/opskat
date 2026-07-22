package tool

import (
	"context"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
)

// extTools 扩展派发：ext_exec。
// 派发逻辑（按 command → 扩展/工具 → 按 asset 做策略检查 → 调 Plugin.CallTool）仍在
// handleExecTool 中，这里只把它包成 cago 原生 tool.Tool。Serial：扩展执行可能跨
// SSH/远程，统一串行避免审计错位。
//
// command 的 flag 语法与 internal/ai/cmdline 的既有约定一致（mongo/kafka DSL 同一份
// 实现）：`--flag=value` 或裸 `--flag`（值为 true），不是空格分隔的 `--flag value`——
// 空格分隔时后一个词会被当成位置参数，而扩展工具的 parameters 里没有位置参数的位置。
func extTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "ext_exec",
			DescStr: "Execute a tool exposed by an installed extension. command is `<extension> <tool> --flag=value`, " +
				"the same shape as exec: the first token is the operation. Flags use --flag=value or a bare --flag " +
				"(true); array parameters take a comma-separated value (--keys=a,b,c). Available extensions and their " +
				"tools are described in any 'From extension: <name>' section of this system prompt — read that " +
				"section first. Use --json='{...}' (single-quoted so embedded double quotes survive parsing) when a " +
				"tool takes a nested structure the flag syntax cannot express; --json takes over the whole call and " +
				"cannot be combined with other flags. Pass asset when the extension is asset-scoped, so policy checks " +
				"can run against that asset's group.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset":   {Type: "string", Description: "Target asset id or name. Required for asset-scoped extensions (those declaring a policy type)."},
					"command": {Type: "string", Description: `Extension command, e.g. "oss list_objects --bucket=my-bucket --maxKeys=100".`},
				},
				Required: []string{"command"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleExecTool(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
