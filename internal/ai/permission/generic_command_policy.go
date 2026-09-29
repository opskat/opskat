package permission

import (
	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
)

// 本地命令执行方式的权限判定（docs/specs/2026-09-28-generic-asset.md「策略、审批与审计」）：
// 由本文件（而不是 generic_policy.go）往 genericModeChecks 表里登记，呼应该表旁的注释
// "本地命令：由命令执行方式在这张表里登记自己的判定（本文件之外）"。
//
// 类型上有命令模板时，匹配对象是 exec 参数串，与 HTTP 共用同一套普通 glob 判定
// （checkPlainGlobPolicy）；没有命令模板时，匹配对象是整条 shell 命令，按 SSH 同款的
// 子命令拆分 + policy.MatchCommandRule 判定（checkShellCommandPolicy，与 checkCommand
// PolicyPermission 共用同一个核心，grant 落在通用资产自己的 "generic" 工具面）。
func init() {
	genericModeChecks[custom_type_entity.ExecModeCommand] = checkCommandModePermission
}

func checkCommandModePermission(ctx context.Context, asset *asset_entity.Asset, ct *custom_type_entity.CustomType, subject string) aictx.CheckResult {
	// ct.Command 非空由 custom_type_entity.Validate 在保存时保证，这里信任这条约束。
	if ct.Command.Template != "" {
		return checkPlainGlobPolicy(ctx, asset, subject)
	}
	var groups []*group_entity.Group
	if asset.GroupID > 0 {
		groups = policy.ResolveGroupChain(ctx, asset.GroupID)
	}
	return checkShellCommandPolicy(ctx, asset.ID, asset, groups, subject, ApprovalTypeFor(asset_entity.AssetTypeGeneric))
}
