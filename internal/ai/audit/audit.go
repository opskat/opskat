// Package audit 提供 AI/opsctl 工具调用的审计写入接口与默认实现。
//
// 命令摘要提取走 [RegisterExtractor] 注册表：审计自带常见工具(run_command/exec_sql 等)
// 的默认提取器；协议特有的(kafka_*, exec_k8s)由各自子包在 init() 中注册。
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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
}

// AuditWriter 审计日志写入接口
type AuditWriter interface {
	WriteToolCall(ctx context.Context, info ToolCallInfo)
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

	assetID := aictx.ArgInt64(args, "asset_id")
	if assetID == 0 {
		assetID = aictx.ArgInt64(args, "id")
	}

	assetName := ""
	if assetID > 0 && asset_repo.Asset() != nil {
		if a, err := asset_repo.Asset().Find(context.Background(), assetID); err == nil {
			assetName = a.Name
		}
	}

	command := ExtractCommandForAudit(info.ToolName, args)

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
