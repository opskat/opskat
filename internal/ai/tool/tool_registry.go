package tool

import (
	"context"

	"github.com/opskat/opskat/internal/ai/helper"

	// execimpl 在 init() 中把各资产类型的纯执行体注册进 permission 的执行器表
	// （供统一 exec 工具派发）。当前有哪些类型以 permission.RegisteredExecTypes() 为准，
	// 这里不再抄一份名单——抄的那份停在 ssh/serial/database/redis/k8s，漏了后来接入的
	// mongodb/etcd/kafka。blank-import 是唯一的触发点：
	// 导入本包的两条路径（桌面端与 opsctl）都会触发这次注册，注册一次即可。
	// 但注册被共用不等于工具被共用——exec/help 目前只在 Tools()（桌面端 AI）里，
	// 下面的 AllToolDefs()（opsctl 派发表）没有它们的条目，所以 opsctl 现在还调不到
	// 统一 exec，只能走按类型的旧工具。
	// execimpl 不导入本包（tool），避免循环依赖。
	_ "github.com/opskat/opskat/internal/ai/execimpl"
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
		{"request_permission", handleRequestGrant},
		{"exec_tool", handleExecTool},
	}
}

// --- SSH 客户端缓存（cago 工具 handler 在同一次 Send 中复用连接）---
//
// 实现已移入 helper（execimpl 需要在不依赖 tool 包的前提下复用同一执行体），
// 这里保留同名导出符号作为薄别名，避免影响 internal/app/ai 等外部调用方。

// SSHClientCache 在同一次 AI Send 中复用 SSH 连接。
type SSHClientCache = helper.SSHClientCache

// NewSSHClientCache 创建 SSH 客户端缓存。
func NewSSHClientCache() *SSHClientCache {
	return helper.NewSSHClientCache()
}

// WithSSHCache 将 SSH 缓存注入 context。
func WithSSHCache(ctx context.Context, cache *SSHClientCache) context.Context {
	return helper.WithSSHCache(ctx, cache)
}
