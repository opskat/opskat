// Package execimpl wires the pure-execution helpers in internal/ai/helper into
// permission's executor registry. It must NOT import internal/ai/tool: tool
// blank-imports execimpl to trigger this registration, and the reverse import
// would create a cycle.
package execimpl

import (
	"context"

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
		}, k8sHelp)
}
