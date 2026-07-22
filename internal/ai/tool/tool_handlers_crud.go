package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// putArgs 把工具入参摊平成 assettype 处理器认识的形态。
//
// 工具契约是 `config` 嵌套对象（spec §4.3），而 assettype.ApplyCreateArgs / ApplyUpdateArgs
// 的契约是扁平 map——后者被桌面端与 opsctl 共用，不该为了 AI 工具的形状改动。
// 摊平只在这一处发生，是 AGENTS.md「在边界归一化一次」的直接应用。
func putArgs(args map[string]any) map[string]any {
	flat := map[string]any{}
	if config, ok := args["config"].(map[string]any); ok {
		for k, v := range config {
			flat[k] = v
		}
	}
	return flat
}

// handlePutAsset 创建或更新资产：带 asset 标识 → 更新，不带 → 创建。
//
// 与被它取代的 add_asset 的关键差异：config 是自由对象，其按类型的形状由该类型的
// SKILL.md（同一份 help 文档）说明，校验回到本就该负责的 assettype.ValidateCreateArgs。
// 旧的巨型 schema 把 10 种类型的字段并集写进 JSON Schema，既是我们正在移除的类型分支的
// 另一种写法，又漏掉了 oss——那类资产此前经 AI 完全无法创建。
func handlePutAsset(ctx context.Context, args map[string]any) (string, error) {
	config := putArgs(args)
	ref := aictx.ArgString(args, "asset")

	if ref == "" {
		return createAsset(ctx, args, config)
	}
	return updateAsset(ctx, ref, args, config)
}

func createAsset(ctx context.Context, args, config map[string]any) (string, error) {
	name := aictx.ArgString(args, "name")
	if name == "" {
		return "", fmt.Errorf("missing required parameter: name (creating an asset needs a name; pass asset=<id-or-name> instead to update an existing one)")
	}
	assetType := aictx.ArgString(args, "type")
	if assetType == "" {
		assetType = asset_entity.AssetTypeSSH
	}
	h, ok := assettype.Get(assetType)
	if !ok {
		return "", fmt.Errorf("unsupported asset type %q; supported: %s",
			assetType, strings.Join(permission.RegisteredHelpTypes(), ", "))
	}
	if err := h.ValidateCreateArgs(config); err != nil {
		return "", err
	}

	asset := &asset_entity.Asset{
		Name:        name,
		Type:        assetType,
		Icon:        aictx.ArgString(args, "icon"),
		GroupID:     aictx.ArgInt64(args, "group_id"),
		Description: aictx.ArgString(args, "description"),
	}
	if err := h.ApplyCreateArgs(ctx, asset, config); err != nil {
		return "", err
	}
	if err := asset_svc.Asset().Create(ctx, asset); err != nil {
		return "", fmt.Errorf("failed to create asset: %w", err)
	}
	aictx.NotifyDataChanged("asset")
	return fmt.Sprintf(`{"id":%d,"message":"asset created successfully"}`, asset.ID), nil
}

func updateAsset(ctx context.Context, ref string, args, config map[string]any) (string, error) {
	asset, err := assetref.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	if declared := aictx.ArgString(args, "type"); declared != "" {
		// 更新时给出的 type 与 exec 的 type 同语义：断言，不是改类型。
		// 资产类型是不可变的——改类型等于换协议、换配置形状、换策略组。
		if err := permission.AssertAssetType(asset, declared); err != nil {
			return "", err
		}
	}

	if name := aictx.ArgString(args, "name"); name != "" {
		asset.Name = name
	}
	if _, ok := args["description"]; ok {
		asset.Description = aictx.ArgString(args, "description")
	}
	// 仅接受正整数：避免误传 group_id=0 把资产悄悄移到未分组（潜在破坏性，走 UI）。
	if gid := aictx.ArgInt64(args, "group_id"); gid > 0 {
		asset.GroupID = gid
	}
	if icon := aictx.ArgString(args, "icon"); icon != "" {
		asset.Icon = icon
	}

	if h, ok := assettype.Get(asset.Type); ok {
		if err := h.ApplyUpdateArgs(ctx, asset, config); err != nil {
			return "", fmt.Errorf("apply update args failed: %w", err)
		}
	}
	if err := asset_svc.Asset().Update(ctx, asset); err != nil {
		return "", fmt.Errorf("failed to update asset: %w", err)
	}
	aictx.NotifyDataChanged("asset")
	return fmt.Sprintf(`{"id":%d,"message":"asset updated successfully"}`, asset.ID), nil
}

// handlePutGroup 创建或更新分组：带 id → 更新，不带 → 创建。
// 分组用数字 id 而非名称——仓内没有 groupref 解析器，get_group / list_groups 一直是数字 id。
func handlePutGroup(ctx context.Context, args map[string]any) (string, error) {
	id := aictx.ArgInt64(args, "id")
	if id == 0 {
		name := aictx.ArgString(args, "name")
		if name == "" {
			return "", fmt.Errorf("missing required parameter: name (creating a group needs a name; pass id=<group id> instead to update an existing one)")
		}
		now := time.Now().Unix()
		group := &group_entity.Group{
			Name:        name,
			ParentID:    aictx.ArgInt64(args, "parent_id"),
			Icon:        aictx.ArgString(args, "icon"),
			Description: aictx.ArgString(args, "description"),
			SortOrder:   aictx.ArgInt(args, "sort_order"),
			Createtime:  now,
			Updatetime:  now,
		}
		if err := group_repo.Group().Create(ctx, group); err != nil {
			return "", fmt.Errorf("failed to create group: %w", err)
		}
		aictx.NotifyDataChanged("group")
		return fmt.Sprintf(`{"id":%d,"message":"group created successfully"}`, group.ID), nil
	}

	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return "", fmt.Errorf("group not found: %w", err)
	}
	if name := aictx.ArgString(args, "name"); name != "" {
		group.Name = name
	}
	// 仅接受正整数：避免误传 parent_id=0 把分组悄悄变成顶级。
	if pid := aictx.ArgInt64(args, "parent_id"); pid > 0 {
		group.ParentID = pid
	}
	if _, ok := args["icon"]; ok {
		group.Icon = aictx.ArgString(args, "icon")
	}
	if _, ok := args["description"]; ok {
		group.Description = aictx.ArgString(args, "description")
	}
	if _, ok := args["sort_order"]; ok {
		group.SortOrder = aictx.ArgInt(args, "sort_order")
	}
	group.Updatetime = time.Now().Unix()
	if err := group_repo.Group().Update(ctx, group); err != nil {
		return "", fmt.Errorf("failed to update group: %w", err)
	}
	aictx.NotifyDataChanged("group")
	return fmt.Sprintf(`{"id":%d,"message":"group updated successfully"}`, group.ID), nil
}
