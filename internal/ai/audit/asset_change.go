package audit

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// 资产增删改的审计动作名，写进 audit_logs.tool_name。
//
// ActionDeleteAsset 与 AI 侧同名工具 delete_asset（internal/ai/tool/tool_handlers_crud.go
// 的 handleDeleteAsset）撞名：桌面（DeleteAsset、以及 DeleteGroup 的级联删除）与 AI
// 两条路径删同一台资产时，落在 audit_logs.tool_name 的值一致，审计查询不必按来源
// 分别写条件。ActionAddAsset / ActionUpdateAsset 不再有这个性质——AI 侧已经把
// add_asset / update_asset 合并成单一的 put_asset 工具，只有桌面仍按增/改分别记名，
// 两侧的 tool_name 从此不一致（AI 一侧的等价记录靠 runner.auditMiddleware 直接用工具名
// "put_asset" 写 ToolCallInfo，不经过本文件）。
const (
	ActionAddAsset    = "add_asset"
	ActionUpdateAsset = "update_asset"
	ActionDeleteAsset = "delete_asset"
)

// assetAuditView 落进 audit_logs.request 的字段白名单。
//
// 是白名单而不是"删掉敏感字段"的黑名单：asset_entity.Asset.Config 是整个连接配置的
// JSON，今天装着 password / credential_id / 代理链口令，明天新增一个字段没人会想起
// 回来改审计。审计要回答的是"谁动了哪台机器"，这四个字段就够。
type assetAuditView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	GroupID int64  `json:"group_id"`
}

// WriteAssetChange 记录一次资产增删改。
//
// 复用 WriteToolCall 这条既有链路（source 取自 ctx、success/error/session 列的填法、
// 截断规则都在那里），而不是另起一个写入口——审计的写入点越多，列语义漂移得越快。
// 调用方负责用 aictx.WithAuditSource 标好来源；桌面 binder 标的是 "desktop"。
func WriteAssetChange(ctx context.Context, action string, asset *asset_entity.Asset, actionErr error) {
	request, err := json.Marshal(assetAuditView{
		ID:      asset.ID,
		Name:    asset.Name,
		Type:    asset.Type,
		GroupID: asset.GroupID,
	})
	if err != nil {
		logger.Ctx(ctx).Error("marshal asset audit request",
			zap.Int64("assetID", asset.ID), zap.Error(err))
		return
	}

	NewDefaultAuditWriter().WriteToolCall(ctx, ToolCallInfo{
		ToolName:  action,
		ArgsJSON:  string(request),
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Error:     actionErr,
	})
}
