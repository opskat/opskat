package tool

import (
	"context"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
)

// assetTools 资产 + 分组的只读工具。写操作已合并进 crudTools() 的 put_asset / put_group
// （见 tools_crud.go）——分支由标识的有无决定，不再由 add_*/update_* 两个几乎同构的
// 工具承担。
// Description/Schema 是给模型看的契约，改字段时同步更新前端/文档。
func assetTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "list_assets",
			DescStr: "List managed remote server assets. Returns an array of assets (with ID, name, type, group, etc.). This is typically the first step to discover asset IDs for other operations. Supports filtering by type and group. Use get_asset to view asset description and connection details.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset_type": {Type: "string", Description: `Filter by asset type. Supported: "ssh", "serial", "rdp", "vnc", "database", "redis", "mongodb", "kafka", "k8s", "etcd". Omit to return all types.`},
					"group_id":   {Type: "number", Description: "Filter by group ID. Omit or set to 0 to list all groups."},
				},
			},
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleListAssets(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "get_asset",
			DescStr: "Get detailed information about a specific asset, including connection fields, asset-type-specific metadata, and safe managed authentication status when present. For k8s assets, inspect namespace, context, and ssh_tunnel_id to decide whether kubectl should run through an SSH jump host. For rdp assets, inspect host, port, username, domain, width, height, and clipboard.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"id": {Type: "number", Description: "Asset ID. Use list_assets to find available IDs."},
				},
				Required: []string{"id"},
			},
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleGetAsset(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "list_groups",
			DescStr: "List all asset groups. Groups organize assets into a hierarchy via parent_id. Use get_group to view group description.",
			SchemaVal: agent.Schema{
				Type: "object",
			},
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleListGroups(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "get_group",
			DescStr: "Get detailed information about a specific asset group, including its description.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"id": {Type: "number", Description: "Group ID. Use list_groups to find available IDs."},
				},
				Required: []string{"id"},
			},
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleGetGroup(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
