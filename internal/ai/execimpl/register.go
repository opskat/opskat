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
	// serial 没有可规范化的命令，所以用 precheck 而不是 canonicalize 达到同一个目的：
	// 见 helper.PrecheckSerialSession 的注释，以及下面 canonicalizeK8sCommand 的注释。
	permission.RegisterPrecheck(asset_entity.AssetTypeSerial, helper.PrecheckSerialSession)

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

	etcdHelp, _ := skills.Get(asset_entity.AssetTypeEtcd)
	permission.RegisterExecutor(asset_entity.AssetTypeEtcd,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecEtcdOnAsset(ctx, asset, command, scope)
		}, etcdHelp, helper.CanonicalizeEtcdCommand)

	mongoHelp, _ := skills.Get(asset_entity.AssetTypeMongoDB)
	permission.RegisterExecutor(asset_entity.AssetTypeMongoDB,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecMongoOnAsset(ctx, asset, command, scope)
		}, mongoHelp, helper.CanonicalizeMongoCommand)

	kafkaHelp, _ := skills.Get(asset_entity.AssetTypeKafka)
	permission.RegisterExecutor(asset_entity.AssetTypeKafka,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecKafkaOnAsset(ctx, asset, command, scope)
		}, kafkaHelp, helper.CanonicalizeKafkaCommand)

	// 没有命令面、但可以被 put_asset 创建/更新的类型：只注册文档。
	// exec 对它们仍然报 "no exec support yet"（RegisteredExecTypes 会跳过 exec == nil 的条目）。
	for _, docOnly := range []string{
		asset_entity.AssetTypeRDP,
		asset_entity.AssetTypeVNC,
		asset_entity.AssetTypeOSS,
		asset_entity.AssetTypeLocal,
	} {
		doc, ok := skills.Get(docOnly)
		if !ok {
			// 文档缺失是编译期就能发现的接线错误（SKILL.md 是 //go:embed 进来的），
			// 静默跳过会让 put_asset 对该类型永远无从查起。
			panic("execimpl: missing SKILL.md for doc-only asset type " + docOnly)
		}
		permission.RegisterHelpDoc(docOnly, doc)
	}
}

// canonicalizeK8sCommand 把原始 kubectl 命令规范化为注入 --context/--namespace 之后的
// effective 命令——这正是已删除的 exec_k8s 工具用来做权限检查、并呈现给审批弹窗与
// 审计日志的形式。统一 exec 复用同一个 BuildK8sCommandPlan，避免换了入口之后校验的
// 字符串变了，导致既有策略/grant 静默失配。
//
// 这里也检查 kubeconfig 是否已配置：canonicalize 排在统一 exec 的权限检查之前
// （internal/ai/tool/tool_handlers_unified.go 的 handleExec），所以这个检查会在审批
// 弹窗弹出之前失败，对齐 exec_k8s 当年在 BuildK8sCommandPlan 之前就做的同一个检查。
// 若把它留在 helper.ExecK8sOnAsset 内部（纯执行体，只在权限检查通过之后才会被调用），
// 用户会先被弹一次审批，批准之后命令才因为没有 kubeconfig 而失败。
func canonicalizeK8sCommand(asset *asset_entity.Asset, command string) (string, error) {
	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get k8s config: %w", err)
	}
	if cfg.Kubeconfig == "" {
		return "", fmt.Errorf("no kubeconfig configured for this k8s asset")
	}
	plan, err := helper.BuildK8sCommandPlan(command, cfg)
	if err != nil {
		return "", err
	}
	return plan.EffectiveCommand, nil
}
