// Package execimpl wires the pure-execution helpers in internal/ai/helper into
// permission's executor registry. It must NOT import internal/ai/tool: tool
// blank-imports execimpl to trigger this registration, and the reverse import
// would create a cycle.
package execimpl

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func init() {
	sshHelp, _ := skills.Get(asset_entity.AssetTypeSSH)
	permission.RegisterExecutor(asset_entity.AssetTypeSSH,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecCommandOnAsset(ctx, asset, command, scope)
		}, sshHelp)

	serialHelp, _ := skills.Get(asset_entity.AssetTypeSerial)
	permission.RegisterExecutor(asset_entity.AssetTypeSerial,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecSerialOnAsset(ctx, asset, command, scope)
		}, serialHelp)

	databaseHelp, _ := skills.Get(asset_entity.AssetTypeDatabase)
	permission.RegisterExecutor(asset_entity.AssetTypeDatabase,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecSQLOnAsset(ctx, asset, command, scope)
		}, databaseHelp)

	redisHelp, _ := skills.Get(asset_entity.AssetTypeRedis)
	permission.RegisterExecutor(asset_entity.AssetTypeRedis,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecRedisOnAsset(ctx, asset, command, scope)
		}, redisHelp)

	k8sHelp, _ := skills.Get(asset_entity.AssetTypeK8s)
	permission.RegisterExecutor(asset_entity.AssetTypeK8s,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecK8sOnAsset(ctx, asset, command, scope)
		}, k8sHelp, canonicalizeK8sCommand)
}

// canonicalizeK8sCommand 把原始 kubectl 命令规范化为注入 --context/--namespace 之后的
// effective 命令——这正是 handleExecK8s（internal/ai/tool/tool_handler_k8s.go）今天用来
// 做权限检查、并呈现给审批弹窗与审计日志的形式。统一 exec 复用同一个 BuildK8sCommandPlan，
// 避免两条路径校验不同字符串导致既有策略/grant 静默失配。
func canonicalizeK8sCommand(asset *asset_entity.Asset, command string) (string, error) {
	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get k8s config: %w", err)
	}
	plan, err := helper.BuildK8sCommandPlan(command, cfg)
	if err != nil {
		return "", err
	}
	return plan.EffectiveCommand, nil
}
