package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_put_svc"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/group_svc"
)

// putArgs 把工具入参摊平成 assettype 处理器认识的形态。
//
// 工具契约是 `config` 嵌套对象（spec §4.3），而 assettype.ApplyCreateArgs / ApplyUpdateArgs
// 的契约是扁平 map——后者被桌面端与 opsctl 共用，不该为了 AI 工具的形状改动。
// 摊平只在这一处发生，是 AGENTS.md「在边界归一化一次」的直接应用。
func putArgs(args map[string]any) (map[string]any, error) {
	raw, exists := args["config"]
	if !exists {
		return map[string]any{}, nil
	}
	config, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parameter config must be an object, got %T", raw)
	}
	flat := make(map[string]any, len(config))
	for k, v := range config {
		flat[k] = v
	}
	return flat, nil
}

// handlePutAsset 创建或更新资产：带 asset 标识 → 更新，不带 → 创建。
//
// 与被它取代的 add_asset 的关键差异：config 是自由对象，其按类型的形状由该类型的
// SKILL.md（同一份 help 文档）说明，校验回到本就该负责的 assettype.ValidateCreateArgs。
// 旧的巨型 schema 把 10 种类型的字段并集写进 JSON Schema，既是我们正在移除的类型分支的
// 另一种写法，又漏掉了 oss——那类资产此前经 AI 完全无法创建。
func handlePutAsset(ctx context.Context, args map[string]any) (string, error) {
	// 一进来就落审计投影的基线：putArgs/校验/lookup 这些 Prepare 之前的早期返回没有
	// producer 投影可用，若不先投影，runner 的 auditMiddleware 会回退到原始 c.Input，
	// 把可能携带 write-only 秘密的 config 原样写进审计。这里只投影顶层非 config 字段
	// （config 是自由对象、可能带秘密）；Prepare/Commit 成功后再用 producer 投影覆盖。
	// 该投影是独立 map，绝不改写 args——执行/审批/ToolBlock/历史仍见原值。
	aictx.RecordAuditRequest(ctx, putAssetTopLevelAuditArgs(args))

	config, err := putArgs(args)
	if err != nil {
		return "", err
	}
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
	if _, ok := assettype.Get(assetType); !ok {
		return "", fmt.Errorf("unsupported asset type %q; supported: %s",
			assetType, strings.Join(permission.RegisteredHelpTypes(), ", "))
	}

	asset := &asset_entity.Asset{
		Name:        name,
		Type:        assetType,
		Icon:        aictx.ArgString(args, "icon"),
		GroupID:     aictx.ArgInt64(args, "group_id"),
		Description: aictx.ArgString(args, "description"),
	}
	req := asset_put_svc.Request{
		Asset:          asset,
		Config:         config,
		CredentialName: aictx.ArgString(args, "credential_name"),
	}
	prepared, err := asset_put_svc.Prepare(ctx, req)
	if err != nil {
		// 顶层基线已在 handlePutAsset 入口记录，这里不重复。
		return "", fmt.Errorf("failed to create asset: %w", err)
	}
	result, err := asset_put_svc.Commit(ctx, prepared)
	if err != nil {
		// commit/仓库失败：Prepare 已产出 producer 投影（SafeAuditArgs），直接落审计。
		aictx.RecordAuditRequest(ctx, prepared.SafeAuditArgs())
		return "", fmt.Errorf("failed to create asset: %w", err)
	}
	aictx.RecordAuditRequest(ctx, prepared.SafeAuditArgsForResult(result))
	aictx.NotifyDataChanged("asset")
	return putAssetResultJSON(result, "asset created successfully")
}

// putAssetTopLevelAuditArgs 把 put_asset 顶层非 config 字段投影给 Audit，作为
// handlePutAsset 入口的基线（record 在 putArgs/校验/lookup 之前）。config 是自由对象、
// 可能携带 write-only 秘密，任何 Prepare 之前的失败都绝不回退原始 config；其余顶层字段
// （资产身份/描述/分组等）至少保留。Prepare/Commit 成功后再用 producer 投影覆盖基线。
func putAssetTopLevelAuditArgs(args map[string]any) map[string]any {
	out := make(map[string]any)
	for _, key := range []string{"asset", "name", "type", "group_id", "description", "icon", "credential_name"} {
		if v, ok := args[key]; ok {
			out[key] = v
		}
	}
	return out
}

func putAssetResultJSON(result *asset_put_svc.Result, message string) (string, error) {
	payload := struct {
		ID             int64                            `json:"id"`
		Authentication *asset_put_svc.AuthenticationRef `json:"authentication,omitempty"`
		Message        string                           `json:"message"`
	}{ID: result.ID, Authentication: result.Authentication, Message: message}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode asset result: %w", err)
	}
	return string(encoded), nil
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

	req := asset_put_svc.Request{
		Asset:          asset,
		Config:         config,
		CredentialName: aictx.ArgString(args, "credential_name"),
	}
	prepared, err := asset_put_svc.Prepare(ctx, req)
	if err != nil {
		// 顶层基线已在 handlePutAsset 入口记录，这里不重复。
		return "", fmt.Errorf("failed to update asset: %w", err)
	}
	result, err := asset_put_svc.Commit(ctx, prepared)
	if err != nil {
		aictx.RecordAuditRequest(ctx, prepared.SafeAuditArgs())
		return "", fmt.Errorf("failed to update asset: %w", err)
	}
	aictx.RecordAuditRequest(ctx, prepared.SafeAuditArgsForResult(result))
	aictx.NotifyDataChanged("asset")
	return putAssetResultJSON(result, "asset updated successfully")
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

// decisionFromApproval maps the shared, kind-aware approval parser onto the audit slot.
// A consumer still requires the exact typed decision it supports; for delete that is
// ApprovalAllow only, so allowAll/unknown/malformed responses stay deny even if a caller
// bypasses the Wails response boundary in a test or a future Go-to-Go path.
func decisionFromApproval(kind string, resp permission.ApprovalResponse) aictx.CheckResult {
	parsed, err := permission.ParseApprovalResponse(kind, resp)
	if err != nil || parsed.Decision != permission.ApprovalAllow {
		return aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}
	}
	return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
}

// auditDeletedAsset writes one delete_asset audit row for an asset that was removed
// as a side effect of deleting its group. Mirrors internal/app/system/asset.go's
// System.DeleteGroup: same action constant, same audit.WriteAssetChange call, ctx
// carries whatever source (desktop/ai/opsctl) the caller already stamped on it — this
// helper doesn't guess.
func auditDeletedAsset(ctx context.Context, asset *asset_entity.Asset) {
	audit.WriteAssetChange(ctx, audit.ActionDeleteAsset, asset, nil)
}

// handleDeleteAsset 删除资产。
//
// 两条与其它工具都不同的规则（spec §4.4）：
//
//  1. **恒需确认**：不查策略、不查 grant，直接调 ConfirmFunc。这不是"检查后发现需要确认"，
//     而是"根本没有可以放行的分支"——与 #249 的修法同款：把放行写不出来，比补一个
//     `if !grantable` 判断更难被后来的改动绕过。
//  2. **不可 grant**："删除这个资产"不是可预批的重复命令模式。既然从不经过策略层，
//     grant 也就无从匹配。前端必须同步用 delete 这个 kind 渲染（不出现"全部允许"按钮），
//     否则 UI 上仍然可以把删除写进 grant，把这条约束当场架空。
//
// 名称在删除**之前**捕获：asset_repo.Find 过滤 status = Active，删完再查就查不到了，
// 而审计中间件（runner.resolveAssetForAudit）本身就在 c.Next() 之前解析 args["asset"]，
// 所以审计行的归属不依赖这里——这里捕获是给审批弹窗与返回值用的。
func handleDeleteAsset(ctx context.Context, args map[string]any) (string, error) {
	asset, err := assetref.Resolve(ctx, aictx.ArgString(args, "asset"))
	if err != nil {
		return "", err
	}

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		// 没有确认回调 = 没有人能点头。删除不存在"无人值守也放行"的形态。
		return "", fmt.Errorf("delete_asset requires an interactive approval channel, none is wired")
	}

	resp := confirm(ctx, permission.ApprovalKindDelete, []permission.ApprovalItem{{
		Type:      permission.ApprovalTypeDelete,
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Command:   fmt.Sprintf("delete asset %q (type=%s)", asset.Name, asset.Type),
		Detail:    "The asset row is soft-deleted and its connection config is cleared — this cannot be undone from the app. Open sessions and pooled connections for this asset are closed.",
	}})
	decision := decisionFromApproval(permission.ApprovalKindDelete, resp)
	aictx.RecordDecision(ctx, decision)
	if decision.Decision != aictx.Allow {
		return fmt.Sprintf("USER DENIED: The user has denied deleting asset %q. Stop the current task immediately.", asset.Name), nil
	}

	if err := asset_svc.Asset().Delete(ctx, asset.ID); err != nil {
		return "", fmt.Errorf("failed to delete asset: %w", err)
	}
	aictx.NotifyDataChanged("asset")
	return fmt.Sprintf(`{"id":%d,"name":%q,"message":"asset deleted"}`, asset.ID, asset.Name), nil
}

// handleDeleteGroup 删除分组。delete_assets 默认 false——那是非破坏性分支
// （分组内资产移入未分组）。true 时连带删除，group_svc 在事务提交之后逐个断连
// 并把被删资产回传，这里逐条写 delete_asset 审计（单删一台机器有审计行，
// 删一个含 20 台机器的分组却只有一行是审计盲区）。
func handleDeleteGroup(ctx context.Context, args map[string]any) (string, error) {
	id := aictx.ArgInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("missing required parameter: id")
	}
	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return "", fmt.Errorf("group not found: %w", err)
	}
	deleteAssets := aictx.ArgBool(args, "delete_assets")

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return "", fmt.Errorf("delete_group requires an interactive approval channel, none is wired")
	}

	command := fmt.Sprintf("delete group %q (assets move to ungrouped)", group.Name)
	detail := "Assets in this group are moved to ungrouped; nothing is deleted besides the group itself."
	if deleteAssets {
		command = fmt.Sprintf("delete group %q AND every asset in it", group.Name)
		detail = "Every asset in this group is soft-deleted and its connection config cleared — this cannot be undone from the app."
	}

	resp := confirm(ctx, permission.ApprovalKindDelete, []permission.ApprovalItem{{
		Type:      permission.ApprovalTypeDelete,
		GroupID:   group.ID,
		GroupName: group.Name,
		Command:   command,
		Detail:    detail,
	}})
	decision := decisionFromApproval(permission.ApprovalKindDelete, resp)
	aictx.RecordDecision(ctx, decision)
	if decision.Decision != aictx.Allow {
		return fmt.Sprintf("USER DENIED: The user has denied deleting group %q. Stop the current task immediately.", group.Name), nil
	}

	deleted, err := group_svc.Group().Delete(ctx, id, deleteAssets)
	if err != nil {
		return "", fmt.Errorf("failed to delete group: %w", err)
	}
	for _, a := range deleted {
		auditDeletedAsset(ctx, a) // 连带删掉的资产逐条补 delete_asset 审计
	}
	aictx.NotifyDataChanged("group")
	return fmt.Sprintf(`{"id":%d,"name":%q,"deleted_assets":%d,"message":"group deleted"}`,
		group.ID, group.Name, len(deleted)), nil
}
