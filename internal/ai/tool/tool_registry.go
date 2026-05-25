package tool

import (
	"context"

	"github.com/opskat/opskat/internal/ai/helper"
	"golang.org/x/crypto/ssh"
)

// opsctl CLI 直接以 (ctx, args)→(string, error) 的形式调用 handler，
// 因此保留下面三个最小抽象：
//   - ToolHandlerFunc：handler 通用签名；
//   - audit.CommandExtractorFunc：审计模块从 args 抽命令摘要的签名（audit.go 用）；
//   - ToolDef + AllToolDefs：opsctl 的 name→handler 派发表（cmd/opsctl/command/handler.go 用）。
//
// 此外保留：
//   - SSH 客户端缓存（同一次 Send 内复用 ssh.Client）；
//   - 参数取值辅助（aictx.ArgString / aictx.ArgInt64 / aictx.ArgInt）。

// ToolHandlerFunc 工具处理函数：从 args map 执行操作并返回纯文本结果。
type ToolHandlerFunc func(ctx context.Context, args map[string]any) (string, error)

// ToolDef opsctl 派发表条目，只保留 (name, handler) 对。
type ToolDef struct {
	Name    string
	Handler ToolHandlerFunc
}

// AllToolDefs 返回 opsctl CLI 派发用的工具列表。
// 它不是 Tools() 的镜像：run_serial_command 依赖桌面端已连接的串口 session；
// batch_command 在 opsctl 中有独立的 batch 子命令入口，不走 name→handler 派发表。
func AllToolDefs() []ToolDef {
	return []ToolDef{
		{"list_assets", handleListAssets},
		{"get_asset", handleGetAsset},
		{"add_asset", handleAddAsset},
		{"update_asset", handleUpdateAsset},
		{"list_groups", handleListGroups},
		{"get_group", handleGetGroup},
		{"add_group", handleAddGroup},
		{"update_group", handleUpdateGroup},
		{"run_command", handleRunCommand},
		{"upload_file", handleUploadFile},
		{"download_file", handleDownloadFile},
		{"exec_sql", helper.HandleExecSQL},
		{"exec_redis", helper.HandleExecRedis},
		{"exec_mongo", helper.HandleExecMongo},
		{"exec_etcd", helper.HandleExecEtcd},
		{"exec_k8s", handleExecK8s},
		{"kafka_cluster", helper.HandleKafkaCluster},
		{"kafka_topic", helper.HandleKafkaTopic},
		{"kafka_consumer_group", helper.HandleKafkaConsumerGroup},
		{"kafka_acl", helper.HandleKafkaACL},
		{"kafka_schema", helper.HandleKafkaSchema},
		{"kafka_connect", helper.HandleKafkaConnect},
		{"kafka_message", helper.HandleKafkaMessage},
		{"request_permission", handleRequestGrant},
		{"exec_tool", handleExecTool},
	}
}

// --- SSH 客户端缓存（cago 工具 handler 在同一次 Send 中复用连接）---

type sshCacheKeyType struct{}

// SSHClientCache 在同一次 AI Send 中复用 SSH 连接。
type SSHClientCache = helper.ConnCache[*ssh.Client]

// NewSSHClientCache 创建 SSH 客户端缓存。
func NewSSHClientCache() *SSHClientCache {
	return helper.NewConnCache[*ssh.Client]("SSH")
}

// WithSSHCache 将 SSH 缓存注入 context。
func WithSSHCache(ctx context.Context, cache *SSHClientCache) context.Context {
	return context.WithValue(ctx, sshCacheKeyType{}, cache)
}

func getSSHCache(ctx context.Context) *SSHClientCache {
	if cache, ok := ctx.Value(sshCacheKeyType{}).(*SSHClientCache); ok {
		return cache
	}
	return nil
}
