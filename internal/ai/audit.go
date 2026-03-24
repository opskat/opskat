package ai

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"time"

	"ops-cat/internal/model/entity/audit_entity"
	"ops-cat/internal/repository/asset_repo"
	"ops-cat/internal/repository/audit_repo"
)

// --- Context keys ---

type auditSourceKey struct{}
type conversationIDKey struct{}
type planSessionIDKey struct{}

// WithAuditSource 注入审计来源
func WithAuditSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, auditSourceKey{}, source)
}

// GetAuditSource 获取审计来源
func GetAuditSource(ctx context.Context) string {
	if v, ok := ctx.Value(auditSourceKey{}).(string); ok {
		return v
	}
	return ""
}

// WithConversationID 注入会话 ID
func WithConversationID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, conversationIDKey{}, id)
}

// GetConversationID 获取会话 ID
func GetConversationID(ctx context.Context) int64 {
	if v, ok := ctx.Value(conversationIDKey{}).(int64); ok {
		return v
	}
	return 0
}

// WithPlanSessionID 注入计划会话 ID
func WithPlanSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, planSessionIDKey{}, id)
}

// GetPlanSessionID 获取计划会话 ID
func GetPlanSessionID(ctx context.Context) string {
	if v, ok := ctx.Value(planSessionIDKey{}).(string); ok {
		return v
	}
	return ""
}

// --- AuditingExecutor ---

// AuditingExecutor 包装 ToolExecutor，自动记录审计日志
type AuditingExecutor struct {
	inner ToolExecutor
}

// NewAuditingExecutor 创建审计执行器
func NewAuditingExecutor(inner ToolExecutor) *AuditingExecutor {
	return &AuditingExecutor{inner: inner}
}

func (a *AuditingExecutor) Execute(ctx context.Context, name string, argsJSON string) (string, error) {
	result, err := a.inner.Execute(ctx, name, argsJSON)

	// 写审计日志（fire-and-forget）
	go a.writeAuditLog(ctx, name, argsJSON, result, err)

	return result, err
}

// Close 代理到 inner
func (a *AuditingExecutor) Close() error {
	if closer, ok := a.inner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (a *AuditingExecutor) writeAuditLog(ctx context.Context, name string, argsJSON string, result string, execErr error) {
	var args map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &args)

	assetID := argInt64(args, "asset_id")
	if assetID == 0 {
		assetID = argInt64(args, "id")
	}

	assetName := ""
	if assetID > 0 && asset_repo.Asset() != nil {
		if a, err := asset_repo.Asset().Find(context.Background(), assetID); err == nil {
			assetName = a.Name
		}
	}

	command := ExtractCommandForAudit(name, args)

	success := 1
	errMsg := ""
	if execErr != nil {
		success = 0
		errMsg = execErr.Error()
	}

	entry := &audit_entity.AuditLog{
		Source:         GetAuditSource(ctx),
		ToolName:       name,
		AssetID:        assetID,
		AssetName:      assetName,
		Command:        command,
		Request:        truncateString(argsJSON, 4096),
		Result:         truncateString(result, 4096),
		Error:          errMsg,
		Success:        success,
		ConversationID: GetConversationID(ctx),
		PlanSessionID:  GetPlanSessionID(ctx),
		Createtime:     time.Now().Unix(),
	}

	if repo := audit_repo.Audit(); repo != nil {
		if err := repo.Create(context.Background(), entry); err != nil {
			log.Printf("audit log write failed: %v", err)
		}
	}
}

// extractCommandForAudit 从工具参数中提取命令信息
func ExtractCommandForAudit(toolName string, args map[string]any) string {
	switch toolName {
	case "run_command":
		if v, ok := args["command"].(string); ok {
			return v
		}
	case "upload_file":
		local, _ := args["local_path"].(string)
		remote, _ := args["remote_path"].(string)
		return "upload " + local + " → " + remote
	case "download_file":
		remote, _ := args["remote_path"].(string)
		local, _ := args["local_path"].(string)
		return "download " + remote + " → " + local
	}
	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// WriteAuditLog 供外部直接写入审计日志（opsctl 路径使用）
func WriteAuditLog(ctx context.Context, entry *audit_entity.AuditLog) {
	if entry.Createtime == 0 {
		entry.Createtime = time.Now().Unix()
	}
	if repo := audit_repo.Audit(); repo != nil {
		if err := repo.Create(context.Background(), entry); err != nil {
			log.Printf("audit log write failed: %v", err)
		}
	}
}
