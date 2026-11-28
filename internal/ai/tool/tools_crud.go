package tool

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"

	"github.com/opskat/opskat/internal/ai/permission"
)

// crudTools 资产/分组的写工具。put_* 合并了旧的 add_*/update_*：分支由标识的有无决定，
// 而不是由两个几乎同构的工具承担。config 是自由对象——它按类型的形状由 help 文档说明
// （同一份文档同时服务 exec / put_asset / help），校验回到 assettype.ValidateCreateArgs。
func crudTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "put_asset",
			DescStr: "Create or update an asset through the registered type contract. Pass asset=<id-or-name> to update; omit it to create. " +
				"Call help with the existing asset or a type name (e.g. help(asset=\"rdp\")) for exact config fields " +
				"(registered types: " + strings.Join(permission.RegisteredHelpTypes(), ", ") + "). Unknown config fields are rejected. " +
				"Plaintext password and OSS secret_access_key are write-only asset-local secrets. credential_id reuses " +
				"an existing type-checked managed credential; conflicting secret sources fail. K8s kubeconfig remains directly encrypted asset " +
				"config. Successful output contains only the asset ID and a safe authentication reference when applicable; never echo secrets.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset":       {Type: "string", Description: "Existing asset id or name to update. Omit to create a new asset."},
					"name":        {Type: "string", Description: "Display name. Required when creating."},
					"type":        {Type: "string", Description: `Asset type when creating (defaults to "ssh"). When updating, this is an assertion — the type of an existing asset cannot be changed.`},
					"group_id":    {Type: "number", Description: "Group ID to assign this asset to. Values <= 0 are ignored."},
					"description": {Type: "string", Description: "Description or notes. Empty string clears it."},
					"icon":        {Type: "string", Description: "Icon name."},
					"config":      {Type: "object", Description: "Type-specific connection fields (host, port, username, credentials, …). Plaintext credential fields are write-only. Call help(asset) for the exact field list of a given type."},
				},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handlePutAsset(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "put_group",
			DescStr: "Create or update an asset group. Pass id to update an existing group; omit it to create. Groups nest via parent_id.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"id":          {Type: "number", Description: "Existing group ID to update. Omit to create."},
					"name":        {Type: "string", Description: "Display name. Required when creating."},
					"parent_id":   {Type: "number", Description: "Parent group ID for nesting. Values <= 0 are ignored."},
					"icon":        {Type: "string", Description: "Icon name. Empty string clears it."},
					"description": {Type: "string", Description: "Description. Empty string clears it."},
					"sort_order":  {Type: "number", Description: "Sort order within the parent; lower comes first."},
				},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handlePutGroup(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "delete_asset",
			DescStr: "Delete an asset. This always asks the user for confirmation and can never be pre-approved via request_permission. " +
				"The row is soft-deleted and its connection config is cleared — it cannot be restored from the app. " +
				"Open sessions and pooled connections for this asset are closed. Credentials linked to it are left orphaned.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset": {Type: "string", Description: "Asset id or name to delete. Use list_assets to find it."},
				},
				Required: []string{"asset"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleDeleteAsset(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "delete_group",
			DescStr: "Delete an asset group. By default the group's assets are moved to ungrouped and survive. " +
				"Pass delete_assets=true to delete them too — that is irreversible from the app. " +
				"This always asks the user for confirmation and can never be pre-approved.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"id":            {Type: "number", Description: "Group ID to delete. Use list_groups to find it."},
					"delete_assets": {Type: "boolean", Description: "Delete the assets in this group as well. Defaults to false (they move to ungrouped)."},
				},
				Required: []string{"id"},
			},
			IsSerial: true,
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleDeleteGroup(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
