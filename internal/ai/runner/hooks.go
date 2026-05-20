package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
)

// auditMiddleware 是 cago tool dispatch 的"around"中间件，负责审计落库。
//
// 工作流：
//  1. 建一个 *aictx.CheckResult slot，挂到 c.ctx（通过 c.WithContext）。
//  2. c.Next() 推链：后续 mw（如 aitool.LocalToolGate）+ 终端 tool.Call 全跑；tool
//     内部用 aictx.RecordDecision(ctx, r) 把决策写进 slot。
//  3. c.Next() 返回后，从 c.Output 抽出文本 / 错误，组合成 audit.ToolCallInfo，
//     起 goroutine 异步写入审计仓库。
//
// 每次调用的状态都保存在当前 ctx 和闭包内，不通过全局索引跨调用共享。
func auditMiddleware(c *agent.ToolContext) {
	slot := &aictx.CheckResult{}
	c.WithContext(aictx.WithCheckResultSlot(c.Context(), slot))

	c.Next()

	argsJSON, err := json.Marshal(c.Input)
	if err != nil {
		logger.Default().Warn("audit middleware marshal input", zap.Error(err))
	}

	result, errVal := extractAuditResult(c.Output)

	info := audit.ToolCallInfo{
		ToolName: c.ToolName,
		ArgsJSON: string(argsJSON),
		Result:   result,
		Error:    errVal,
		Decision: slot,
	}
	auditCtx := detachAuditContext(c.Context())
	go auditWriter.WriteToolCall(auditCtx, info)
}

// extractAuditResult 把 cago 的 *ToolResultBlock 拆成审计需要的 (result, error)。
// IsError=true 的块当成 error 路径，文本作为 error message。
func extractAuditResult(block *agent.ToolResultBlock) (string, error) {
	if block == nil {
		return "", nil
	}
	var b strings.Builder
	for _, c := range block.Content {
		switch v := c.(type) {
		case agent.TextBlock:
			b.WriteString(v.Text)
		case *agent.TextBlock:
			if v != nil {
				b.WriteString(v.Text)
			}
		}
	}
	text := b.String()
	if block.IsError {
		if text == "" {
			text = "tool call failed"
		}
		return "", errors.New(text)
	}
	return text, nil
}

// detachAuditContext 把审计需要的 ctx value 复制到一个全新的 context.Background()
// 之上，避免 runner ctx 被 Cancel 后导致审计写入异步中断。
func detachAuditContext(ctx context.Context) context.Context {
	out := context.Background()
	if v := aictx.GetAuditSource(ctx); v != "" {
		out = aictx.WithAuditSource(out, v)
	}
	if v := aictx.GetConversationID(ctx); v != 0 {
		out = aictx.WithConversationID(out, v)
	}
	if v := aictx.GetGrantSessionID(ctx); v != "" {
		out = aictx.WithGrantSessionID(out, v)
	}
	if v := aictx.GetSessionID(ctx); v != "" {
		out = aictx.WithSessionID(out, v)
	}
	return out
}
