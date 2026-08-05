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
	permission.RegisterExecutor(asset_entity.AssetTypeSSH,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecCommandOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeSSH))

	permission.RegisterExecutor(asset_entity.AssetTypeSerial,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecSerialOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeSerial))
	// serial 没有可规范化的命令，所以用 precheck 而不是 canonicalize 达到同一个目的：
	// 见 helper.PrecheckSerialSession 的注释，以及下面 canonicalizeK8sCommand 的注释。
	permission.RegisterPrecheck(asset_entity.AssetTypeSerial, helper.PrecheckSerialSession)

	permission.RegisterExecutor(asset_entity.AssetTypeDatabase,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecSQLOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeDatabase))

	permission.RegisterExecutor(asset_entity.AssetTypeRedis,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecRedisOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeRedis))

	permission.RegisterExecutor(asset_entity.AssetTypeK8s,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecK8sOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeK8s), canonicalizeK8sCommand)

	permission.RegisterExecutor(asset_entity.AssetTypeEtcd,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecEtcdOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeEtcd), helper.CanonicalizeEtcdCommand)

	permission.RegisterExecutor(asset_entity.AssetTypeMongoDB,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecMongoOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeMongoDB), helper.CanonicalizeMongoCommand)

	permission.RegisterExecutor(asset_entity.AssetTypeKafka,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecKafkaOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeKafka), helper.CanonicalizeKafkaCommand)

	permission.RegisterExecutor(asset_entity.AssetTypeOSS,
		func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
			return helper.ExecOSSOnAsset(ctx, asset, command, scope)
		}, mustSkillDoc(asset_entity.AssetTypeOSS), helper.CanonicalizeOSSCommand)
	// OSS 是唯一一条命令请求多项权限的类型（`object move` 同时读源、写目的、删源），
	// 所以它比其它类型多一条接线：permission 只声明 RegisterPolicyStrings 这个入口，
	// 派生函数由这里推上来。方向是被逼出来的而不是选的——internal/ai/helper 已经
	// import permission（transfer_ssh.go 取 GrantToolCp），permission 反向 import
	// helper 去调 ParseOSSCommand 就是 import cycle（设计 §4.3 / D8 的实施期更正）。
	//
	// 漏掉这一行**不会编译报错也不会 panic**：checkOSSPermission 把"派生不出策略串"
	// 当解析失败处理并退回 NeedConfirm，于是生产路径上每一条 OSS 命令都要弹一次审批，
	// §4.1 的 allow / deny 两种判定永远到不了。fail-closed，但也彻底失效。
	// coverage_test.go 的 TestEveryPolicyStringDerivingTypeHasItsDeriver 守住它。
	permission.RegisterPolicyStrings(asset_entity.AssetTypeOSS, func(command string) ([]string, error) {
		c, err := helper.ParseOSSCommand(command)
		if err != nil {
			return nil, err
		}
		return c.PolicyStrings()
	})

	// 没有命令面、但可以被 put_asset 创建/更新的类型：只注册文档。
	// exec 对它们仍然报 "no exec support yet"（RegisteredExecTypes 会跳过 exec == nil 的条目）。
	for _, docOnly := range []string{
		asset_entity.AssetTypeRDP,
		asset_entity.AssetTypeVNC,
		asset_entity.AssetTypeLocal,
	} {
		permission.RegisterHelpDoc(docOnly, mustSkillDoc(docOnly))
	}
}

// mustSkillDoc 返回某资产类型内嵌的 SKILL.md 正文，缺失时直接 panic。
//
// 这是本文件所有 12 处注册（9 个 exec 类型 + 3 个 doc-only 类型）取 help 文档的唯一
// 入口，取代了曾经的 `doc, _ := skills.Get(assetType)`——当时 8 处 exec 类型全部丢弃了
// skills.Get 的第二个返回值，SKILL.md 缺失时会静默把一个空字符串喂给
// permission.RegisterExecutor：HelpFor 依然返回 ("", true)（entry 存在，只是内容为
// 空），help_coverage_test.go 的 TestEveryAssetTypeHasHelpDoc 只检查 ok、不检查内容，
// 于是测试保持全绿，模型却拿不到任何用法文档。
//
// 文档缺失本身是编译期就能发现的接线错误（SKILL.md 是 //go:embed 进来的），因此这里
// panic 而不是把空字符串传下去——permission.RegisterExecutor / RegisterHelpDoc 也各自
// 对 help == "" 兜底一次（属于该包自身的不变式），但那道防线不能替代这里：如果这里继续
// 丢弃 ok，两道防线中只有第二道生效，第一道形同虚设，且首次触发时的 panic 信息
// （"permission: invalid executor registration"）不会点名是哪个资产类型、哪个文件缺失。
func mustSkillDoc(assetType string) string {
	doc, ok := skills.Get(assetType)
	if !ok {
		panic("execimpl: missing SKILL.md for asset type " + assetType)
	}
	return doc
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
