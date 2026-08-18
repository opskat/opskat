package helper

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ExecMongoOnAsset is the permission-check-free execution entry point used by the
// unified exec tool — the only path to MongoDB since the exec_mongo tool was deleted.
// scope is meaningless for MongoDB and is ignored — the database is named by the
// command's --db flag (see internal/ai/skills/mongodb/SKILL.md).
func ExecMongoOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	c, err := resolveMongoCommand(asset, command)
	if err != nil {
		return "", err
	}

	client, closer, err := getOrDialMongoDB(ctx, asset)
	if err != nil {
		return "", fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	// 没有连接缓存时这次调用独占这条连接，必须自己关；有缓存时缓存负责生命周期
	// （与 ExecRedisOnAsset 的处理一致）。
	if getMongoDBCache(ctx) == nil {
		if client != nil {
			defer func() {
				if err := client.Close(); err != nil {
					logger.Default().Warn("close MongoDB connection", zap.Error(err))
				}
			}()
		}
		if closer != nil {
			defer func() {
				if err := closer.Close(); err != nil && !IsExpectedCloseErr(err) {
					logger.Default().Warn("close MongoDB tunnel", zap.Error(err))
				}
			}()
		}
	}

	return ExecuteMongoDB(ctx, client, c.Database, c.Collection, c.Op, c.Query)
}

// CanonicalizeMongoCommand 把模型给的命令规范化为"真正会被执行、也应被策略匹配"的
// 形式：解析 + 注入资产默认库 + Render。
//
// 输出是完整命令串而不是裸 operation token：审批弹窗、策略匹配与审计看到的都是它，
// 而 `deleteMany logs` 与 `deleteMany logs --query='{"filter":{"level":"debug"}}'`
// 是完全不同的两件事（前者清空整个集合，internal/ai/helper/mongodb_helper.go 里
// filter 为空时默认成 bson.D{}）——只显示 "deleteMany" 的弹窗无法让用户区分。
// 策略侧因此改用 op 感知的 policy.MatchMongoRule，裸 op 规则照样命中富命令串。
//
// 排在权限检查之前（handleExec 的顺序，见 internal/ai/tool/tool_handlers_unified.go）：
// 语法错误必然导致执行失败，必须在弹审批对话框之前失败。
func CanonicalizeMongoCommand(asset *asset_entity.Asset, command string) (string, error) {
	c, err := resolveMongoCommand(asset, command)
	if err != nil {
		return "", err
	}
	return c.Render()
}

// resolveMongoCommand 解析命令并补上资产配置里的默认库——ParseMongoCommand 本身不碰
// 资产（它只做词法），默认库是这一层的事。
//
// 规范化路径与执行路径必须都调它：统一 exec 把**原始**命令交给执行器、把**规范化后**
// 的串交给权限检查（见 handleExec 的顺序注释），只在一边注入会让"用户批准的命令"
// 与"实际执行的命令"在库名上分叉。k8s 的 --context/--namespace 注入也是这个形状
// （canonicalizeK8sCommand 与 ExecK8sOnAsset 各自跑一次 BuildK8sCommandPlan）。
//
// 命令显式写了 --db 就不覆盖。资产没配默认库、命令也没写 --db 时，对一个
// needsDatabase 的操作直接在这里报错，而不是留一个空 Database 混过 Render/权限检查——
// 那样的命令下面每个 mongoXxx 执行函数都会因 database 为空而报错，属于"这条命令
// 已经能证明必然失败"的一类，必须在权限检查（可能弹审批对话框）之前失败，理由与
// canonicalize 排在权限检查之前的顺序注释（handleExec，internal/ai/tool/tool_handlers_unified.go）
// 完全一致：不这样做，用户会先被弹一次审批（"allow all" 甚至落一条常驻 grant），
// 批准之后命令才因为这个必然会发生的检查而失败。
func resolveMongoCommand(asset *asset_entity.Asset, command string) (*MongoCommand, error) {
	c, err := ParseMongoCommand(command)
	if err != nil {
		return nil, err
	}
	if c.Database == "" {
		cfg, err := asset.GetMongoDBConfig()
		if err != nil {
			return nil, fmt.Errorf("get mongodb config: %w", err)
		}
		c.Database = cfg.Database
	}
	if c.Database == "" && mongoOps[c.Op].needsDatabase {
		return nil, fmt.Errorf("operation %q requires a database: pass --db=<database> or configure a default database on the asset", c.Op)
	}
	return c, nil
}
