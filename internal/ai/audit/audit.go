// Package audit 提供审计写入接口与默认实现：AI / opsctl 的工具调用，以及桌面 binder
// 的资产增删改（[WriteAssetChange]）。三条来源共用同一套列语义，靠 audit_logs.source
// 区分（"ai" / "opsctl" / "desktop"，取自 ctx 里的 aictx.WithAuditSource）。
//
// 命令摘要提取走 [RegisterExtractor] 注册表：审计在 extractor_default.go 的 init()
// 里为每个工具(exec/upload_file 等)注册默认提取器；需要资产上下文才能算出规范化命令
// 的类型（k8s/etcd/mongo/kafka）由 runner.auditMiddleware 直接覆盖 ToolCallInfo.Command，
// 不经过本注册表（见 [ExtractCommandForAudit] 的注释）。
// 每个工具名各自注册，不互相借用——借用会让一个工具的审计取决于另一个工具是否还在。
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

// ToolCallInfo 一次工具调用的完整信息
type ToolCallInfo struct {
	ToolName string
	ArgsJSON string
	Result   string
	Error    error
	Decision *aictx.CheckResult // 可选，权限检查结果

	// AssetID / AssetName 允许调用方预先解析好资产归属，优先于下面
	// args["asset_id"]/args["id"]/args["asset"] 的默认解析。exec/help 的资产标识是
	// args["asset"]（数字 id 或**名称**字符串），名称必须走 assetref.Resolve；
	// 而且要在工具执行前解析完成——asset_repo 按 status=Active 过滤，执行后再查
	// （如未来的 delete_asset）会因为资产已经被改状态而查不到，名称就丢了。
	// package audit 不能直接依赖 assetref/permission（permission 已经依赖 audit 做
	// 权限检查审计，直接依赖会成环），所以解析放在同时依赖两者、且能拿到 ctx 的
	// runner.auditMiddleware 里，在 c.Next() 之前完成，结果通过这两个字段传进来。
	// 零值表示调用方没有预先解析，走 args 解析路径（opsctl 是这条路径：它自己
	// resolveAsset 之后传的是数字 id，numericAssetRef 认得）。args["id"] 这一档
	// 对注册过 [RegisterGroupScopedTool] 的工具名不生效——那些工具的 "id" 命名的是
	// 分组，不是资产，见该函数的文档注释。
	AssetID   int64
	AssetName string

	// Command 允许调用方预先算好命令摘要，覆盖 ExtractCommandForAudit 的默认解析。
	// 目前只有 auditMiddleware 给 exec 工具填：资产类型注册了 CanonicalizeFunc 时
	// （k8s 注入 --context/--namespace；etcd/mongo/kafka 走各自 DSL 的 round trip，
	// 规范化大小写、复合命令拼写与 flag 顺序），这里存规范化后、真正过了权限
	// 检查/审批弹窗展示的命令，而不是模型传入的原始字符串——否则审计会跟
	// 审批弹窗对不上。
	//
	// kafka 是这里最需要留意的一个：它的规范化结果是策略层的**双 token 串**
	// （如 `topic.delete orders`），不是模型写的富命令串（`topic delete orders`）。
	// 落进 audit_logs.command 的是前者——与审批弹窗展示、与策略规则匹配的是同一个串。
	// 空字符串表示未提供，回退到 ExtractCommandForAudit。
	Command string
}

// AuditWriter 审计日志写入接口
type AuditWriter interface {
	WriteToolCall(ctx context.Context, info ToolCallInfo)
}

// groupScopedTools 记录哪些工具的数字 args["id"] 命名的是**分组**而不是资产。
//
// get_asset 也用 args["id"] 装资产 id（同一个参数名，不同工具里指两种不同实体）——
// WriteToolCall 光看参数名分不清"这个 3 是资产 3 还是分组 3"。get_group/put_group/
// delete_group 在自己的 init() 里把工具名注册进这张表（见 extractor_default.go），
// WriteToolCall 只查表，不按工具名 switch/if-else（AGENTS.md 禁止在共享代码里按类型/
// 名字分支；这与同文件已有的 RegisterExtractor 是同一种"注册而不是分支"的解法）。
//
// 只做到"不误判"：audit_logs 目前没有 group_id/group_name 列，把分组归属真正记下来
// 需要加列（迁移 + 实体 + 前端渲染），超出了这里要修的问题范围——修复后这些工具的
// 审计行 asset_id/asset_name 就是空，而不是编造一个恰好同 id 的无关资产。
var (
	groupScopedMu    sync.RWMutex
	groupScopedTools = map[string]bool{}
)

// RegisterGroupScopedTool 把 toolName 标记为"分组作用域"。
func RegisterGroupScopedTool(toolName string) {
	groupScopedMu.Lock()
	defer groupScopedMu.Unlock()
	groupScopedTools[toolName] = true
}

func isGroupScopedTool(toolName string) bool {
	groupScopedMu.RLock()
	defer groupScopedMu.RUnlock()
	return groupScopedTools[toolName]
}

// DefaultAuditWriter 默认审计日志写入实现
type DefaultAuditWriter struct{}

// NewDefaultAuditWriter 创建默认审计写入器
func NewDefaultAuditWriter() *DefaultAuditWriter {
	return &DefaultAuditWriter{}
}

// WriteToolCall 写入一次工具调用的审计日志
func (w *DefaultAuditWriter) WriteToolCall(ctx context.Context, info ToolCallInfo) {
	var args map[string]any
	if err := json.Unmarshal([]byte(info.ArgsJSON), &args); err != nil {
		logger.Default().Warn("unmarshal audit args", zap.Error(err))
	}

	assetID := info.AssetID
	assetName := info.AssetName
	if assetID == 0 {
		assetID = aictx.ArgInt64(args, "asset_id")
		if assetID == 0 && !isGroupScopedTool(info.ToolName) {
			assetID = aictx.ArgInt64(args, "id")
		}
		if assetID == 0 {
			assetID = numericAssetRef(args)
		}

		assetName = ""
		if assetID > 0 && asset_repo.Asset() != nil {
			if a, err := asset_repo.Asset().Find(context.Background(), assetID); err == nil {
				assetName = a.Name
			}
		}
	}

	command := info.Command
	if command == "" {
		command = ExtractCommandForAudit(info.ToolName, args)
	}

	success := 1
	errMsg := ""
	if info.Error != nil {
		success = 0
		errMsg = info.Error.Error()
	}

	entry := &audit_entity.AuditLog{
		Source:         aictx.GetAuditSource(ctx),
		ToolName:       info.ToolName,
		AssetID:        assetID,
		AssetName:      assetName,
		Command:        command,
		Request:        truncateString(info.ArgsJSON, 4096),
		Result:         truncateString(info.Result, 32768),
		Error:          errMsg,
		Success:        success,
		ConversationID: aictx.GetConversationID(ctx),
		GrantSessionID: aictx.GetGrantSessionID(ctx),
		SessionID:      aictx.GetSessionID(ctx),
		Createtime:     time.Now().Unix(),
	}

	if info.Decision != nil && info.Decision.DecisionSource != "" {
		entry.Decision = info.Decision.DecisionString()
		entry.DecisionSource = info.Decision.DecisionSource
		entry.MatchedPattern = info.Decision.MatchedPattern
	}

	if repo := audit_repo.Audit(); repo != nil {
		if err := repo.Create(context.Background(), entry); err != nil {
			logger.Default().Error("audit log write failed", zap.Error(err))
		}
	}
}

// numericAssetRef 从 exec/help 的 args["asset"] 里取出数字资产 id，取不出返回 0。
//
// exec/help 的资产标识是一个既可以是数字 id、也可以是名称的字符串。名称要
// assetref.Resolve 才能解析，package audit 依赖不了它（会与 permission 成环），
// 所以预解析的调用方走 ToolCallInfo.AssetID（runner.auditMiddleware 就是这么做的）。
// 但**数字**形式在这里就能认出来——opsctl 的 exec/batch 命令自己 resolveAsset 之后
// 把数字 id 塞进 args["asset"]，不认它这些审计行的 asset_id 会全是 0（按类型区分的
// 旧工具下线前传的是 args["asset_id"]，所以以前不会）。
//
// 用 strconv 而不是 aictx.ArgInt64：后者遇到解析不了的字符串会打一条 Warn 日志，
// 而"asset 是个名称"在这里是完全正常的情况，不该刷日志。
func numericAssetRef(args map[string]any) int64 {
	ref, ok := args["asset"].(string)
	if !ok {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// WriteGrantSubmitAudit 记录会话级"始终允许"模式变更
func WriteGrantSubmitAudit(ctx context.Context, assetID int64, assetName string, patterns []string) {
	if repo := audit_repo.Audit(); repo != nil {
		entry := &audit_entity.AuditLog{
			Source:     aictx.GetAuditSource(ctx),
			ToolName:   "grant_submit",
			AssetID:    assetID,
			AssetName:  assetName,
			Command:    strings.Join(patterns, ", "),
			SessionID:  aictx.GetSessionID(ctx),
			Decision:   "allow",
			Success:    1,
			Createtime: time.Now().Unix(),
		}
		if err := repo.Create(context.Background(), entry); err != nil {
			logger.Default().Error("write grant submit audit", zap.Error(err))
		}
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...[truncated]"
}

// LimitedBuffer 限制大小的缓冲区，用于审计日志捕获输出
type LimitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

// NewLimitedBuffer 创建限制大小的缓冲区
func NewLimitedBuffer(limit int) *LimitedBuffer {
	return &LimitedBuffer{limit: limit}
}

func (b *LimitedBuffer) Write(p []byte) (int, error) {
	n := len(p) // 始终返回原始长度，避免 io.MultiWriter 报 ErrShortWrite
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		return n, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.buf.Write(p)
	return n, nil
}

// String 返回缓冲区内容
func (b *LimitedBuffer) String() string {
	return b.buf.String()
}
