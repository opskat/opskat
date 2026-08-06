package tool

import (
	"context"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
)

// execTools 文件传输 / 批量 / grant 申请。
// 命令类工具（cp）标 Serial：跟原"整轮串行"语义对齐，
// 防止同会话内并发产生不可预期的资源争用（同一 SSH 连接复用、SFTP 句柄、审计排序等）。
// request_permission 不直接执行命令，但语义上属于"重操作触发面板"，沿用 Serial 以保证审批弹窗串行可控。
func execTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "cp",
			DescStr: "Copy one file between two endpoints. An endpoint is either an absolute local path " +
				"(/tmp/app.log) or <asset>:/<path> on an asset — an SSH server over SFTP " +
				"(web-01:/var/log/app.log; SSH also accepts <asset>:~/<path>) or object storage as /<bucket>/<key> " +
				"(s3-prod:/backups/db.sql.gz). At least one endpoint must be on an asset; any combination " +
				"of the two sides works, including server to object storage. Each asset endpoint is " +
				"authorized separately under that asset's own policy, before any byte is transferred. " +
				"Credentials are resolved automatically.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"src": {Type: "string", Description: "Source endpoint: an absolute local path, <asset>:/<path>, " +
						"or <asset>:~/<path> for an SSH asset."},
					"dst": {Type: "string", Description: "Destination endpoint, same syntax as src. It names the " +
						"file to write, including the filename."},
					"recursive": {Type: "boolean", Description: "Transfer a directory tree / object prefix instead " +
						"of a single file. With recursive, or when src contains a glob pattern (* ? [), dst must " +
						"end with \"/\" and each entry lands under it."},
				},
				Required: []string{"src", "dst"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleCp(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "batch_exec",
			DescStr: "Execute commands on multiple assets in parallel. Dispatches each item by that asset's real type — the same coverage as exec, including database/redis/mongodb/etcd/kafka/k8s. Always include each item's canonical type when known so a wrong target is caught before execution. Each command is policy-checked; items needing user confirmation are batched into a single approval prompt. Results are returned per-asset (success or error). Prefer this over looping exec calls when targeting >1 asset.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"commands": {Type: "string", Description: `JSON array of commands. Each item: {"asset": "name-or-id", "command": "...", "type": "<canonical asset type>"}. Always include type when known; omit it only when genuinely unknown. type is an assertion and never used for dispatch. Example: [{"asset":"web-1","type":"ssh","command":"uptime"},{"asset":"42","type":"database","command":"SELECT VERSION()"}]`},
				},
				Required: []string{"commands"},
			},
			// batch 内部自己做并发控制（max 10），父级 dispatcher 不需要再串行。
			IsSerial: false,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleBatchCommand(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "request_permission",
			DescStr: "Request approval for grant of command patterns BEFORE executing them. Submit command patterns (one per line, supports '*' wildcard) for one or more target assets. The user will review and may edit the patterns before approving. Once approved, subsequent exec calls matching any approved pattern will be auto-approved.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"items":  {Type: "string", Description: `JSON array of items. Each item: {"asset_id": <number>, "command_patterns": "<patterns separated by newline>"}. Example: [{"asset_id":1,"command_patterns":"cat /var/log/*\nsystemctl * nginx"},{"asset_id":2,"command_patterns":"SELECT * FROM users"}]`},
					"reason": {Type: "string", Description: "Brief explanation of why these permissions are needed."},
				},
				Required: []string{"items", "reason"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleRequestGrant(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
