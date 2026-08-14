package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/app/i18n"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// makeCommandConfirmFunc 创建统一审批回调，向 AI 聊天流发送 approval_request 事件并阻塞等待
func (a *AI) makeCommandConfirmFunc() permission.CommandConfirmFunc {
	return func(ctx context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
		convID := aictx.GetConversationID(ctx)
		if convID == 0 {
			convID = a.currentConversationID // fallback
		}
		confirmID := fmt.Sprintf("ai_%d_%d", convID, time.Now().UnixNano())
		eventName := fmt.Sprintf("ai:event:%d", convID)

		// 发往 Wails 的 items 只用安全投影；后端 pending 保留原始 items 用于响应校验与执行。
		safeItems, redacted := permission.SafeApprovalItems(items)
		wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
			Type:      "approval_request",
			Kind:      kind,
			Items:     safeItems,
			ConfirmID: confirmID,
		})

		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store(confirmID, pendingAIApproval{kind: kind, items: items, redacted: redacted, ch: ch})
		defer a.pendingAIApprovals.Delete(confirmID)

		select {
		case resp := <-ch:
			wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
				Type:      "approval_result",
				ConfirmID: confirmID,
				Content:   resp.Decision,
			})
			return resp
		case <-ctx.Done():
			wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
				Type:      "approval_result",
				ConfirmID: confirmID,
				Content:   "deny",
			})
			return permission.ApprovalResponse{Decision: "deny"}
		case <-a.ctx.Done():
			return permission.ApprovalResponse{Decision: "deny"}
		case <-a.appCtx.Done():
			return permission.ApprovalResponse{Decision: "deny"}
		}
	}
}

// makeGrantRequestFunc 创建 Grant 审批回调，使用 inline approval
func (a *AI) makeGrantRequestFunc() permission.GrantRequestFunc {
	return func(ctx context.Context, items []permission.ApprovalItem, reason string) (bool, []string) {
		convID := aictx.GetConversationID(ctx)
		if convID == 0 {
			convID = a.currentConversationID // fallback
		}
		confirmID := fmt.Sprintf("grant_%d_%d", convID, time.Now().UnixNano())
		eventName := fmt.Sprintf("ai:event:%d", convID)

		safeItems, redacted := permission.SafeApprovalItems(items)
		wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
			Type:        "approval_request",
			Kind:        permission.ApprovalKindGrant,
			Items:       safeItems,
			ConfirmID:   confirmID,
			Description: reason,
			SessionID:   fmt.Sprintf("conv_%d", convID),
		})

		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store(confirmID, pendingAIApproval{kind: permission.ApprovalKindGrant, items: items, redacted: redacted, ch: ch})
		defer a.pendingAIApprovals.Delete(confirmID)

		select {
		case resp := <-ch:
			wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
				Type:      "approval_result",
				ConfirmID: confirmID,
				Content:   resp.Decision,
			})
			parsed, err := permission.ParseApprovalResponse(permission.ApprovalKindGrant, resp, items)
			if err != nil || parsed.Decision != permission.ApprovalAllow {
				return false, nil
			}
			// 脱敏主体不允许落成持久授权：<redacted> 不能成为 pattern，秘密也不能回传持久化。
			if !permission.CanPersistGrant(redacted, permission.ApprovalKindGrant, parsed) {
				return false, nil
			}
			var finalPatterns []string
			sessionID := fmt.Sprintf("conv_%d", convID)
			if len(parsed.EditedItems) > 0 {
				for _, item := range parsed.EditedItems {
					cmd := strings.TrimSpace(item.Command)
					if cmd != "" {
						finalPatterns = append(finalPatterns, cmd)
						// 用户在弹窗里手写/改写的 pattern：他写的通配就是他要的授权范围，
						// 归一化不该收窄它（见 permission.GrantOrigin）。
						permission.SaveGrantPatternsForApproval(i18n.Ctx(a.ctx, a.lang.Lang()), sessionID, item.AssetID, item.AssetName, item.Type, cmd, permission.GrantOriginUser)
					}
				}
			} else {
				for _, item := range items {
					finalPatterns = append(finalPatterns, item.Command)
					// 用户原样批准了系统交上来的主体，没有改写：按系统来源归一化。
					permission.SaveGrantPatternsForApproval(i18n.Ctx(a.ctx, a.lang.Lang()), sessionID, item.AssetID, item.AssetName, item.Type, item.Command, permission.GrantOriginSystem)
				}
			}
			return true, finalPatterns
		case <-ctx.Done():
			wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
				Type:      "approval_result",
				ConfirmID: confirmID,
				Content:   "deny",
			})
			return false, nil
		case <-a.ctx.Done():
			return false, nil
		case <-a.appCtx.Done():
			return false, nil
		}
	}
}

// WindowActivator 由 system binder 实现：审批弹窗时把窗口拉到前台。
type WindowActivator interface {
	ActivateWindow()
}

// SetWindowActivator 由 main.go 注入：local-tool 审批弹出时需要把窗口拉前台。
func (a *AI) SetWindowActivator(w WindowActivator) { a.window = w }

// safeLocalApprovalPatterns 返回可发往 Wails 的本地工具默认 pattern 副本。
// command/detail 一旦发生脱敏，pattern 编辑器按协议必须消失，原始 pattern 也没有
// 继续跨边界的用途；直接省略，避免隐藏 UI 仍把秘密留在事件/store 中。
func safeLocalApprovalPatterns(patterns []string, redacted bool) []string {
	if redacted {
		return nil
	}
	return append([]string(nil), patterns...)
}

// makeLocalToolConfirmFunc 创建 coding agent 本地工具审批回调。
func (a *AI) makeLocalToolConfirmFunc() tool.LocalToolConfirmFunc {
	return func(ctx context.Context, req tool.LocalToolApprovalRequest) permission.ApprovalResponse {
		convID := aictx.GetConversationID(ctx)
		if convID == 0 {
			convID = a.currentConversationID
		}
		confirmID := fmt.Sprintf("local_tool_%d_%d", convID, time.Now().UnixNano())
		eventName := fmt.Sprintf("ai:event:%d", convID)

		if a.window != nil {
			a.window.ActivateWindow()
		}
		approvalItems := []permission.ApprovalItem{{
			Type:    req.ToolName,
			Command: req.Command,
			Detail:  req.Detail,
		}}
		safeItems, redacted := permission.SafeApprovalItems(approvalItems)
		wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
			Type:      "approval_request",
			Kind:      permission.ApprovalKindLocalTool,
			ConfirmID: confirmID,
			ToolName:  req.ToolName,
			Items:     safeItems,
			Patterns:  safeLocalApprovalPatterns(req.DefaultPatterns, redacted),
		})

		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store(confirmID, pendingAIApproval{kind: permission.ApprovalKindLocalTool, items: approvalItems, redacted: redacted, ch: ch})
		defer a.pendingAIApprovals.Delete(confirmID)

		select {
		case resp := <-ch:
			wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
				Type:      "approval_result",
				ConfirmID: confirmID,
				Content:   resp.Decision,
			})
			return resp
		case <-ctx.Done():
			wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
				Type:      "approval_result",
				ConfirmID: confirmID,
				Content:   "deny",
			})
			return permission.ApprovalResponse{Decision: "deny"}
		case <-a.ctx.Done():
			return permission.ApprovalResponse{Decision: "deny"}
		case <-a.appCtx.Done():
			return permission.ApprovalResponse{Decision: "deny"}
		}
	}
}

// RespondAIApproval 前端响应 AI 审批请求（统一入口）
func (a *AI) RespondAIApproval(confirmID string, resp permission.ApprovalResponse) {
	if v, ok := a.pendingAIApprovals.Load(confirmID); ok {
		pending := v.(pendingAIApproval)
		if parsed, err := permission.ParseApprovalResponse(pending.kind, resp, pending.items); err != nil {
			logger.Ctx(a.ctx).Warn("invalid AI approval response denied",
				zap.String("confirmID", confirmID), zap.String("kind", pending.kind),
				zap.String("decision", resp.Decision), zap.Error(err))
			resp = permission.ApprovalResponse{Decision: "deny"}
		} else if !permission.CanPersistGrant(pending.redacted, pending.kind, parsed) {
			// 脱敏主体不允许 allowAll / edited_items：拒绝伪造响应，防止 <redacted> 或秘密落成授权。
			logger.Ctx(a.ctx).Warn("redacted AI approval subject cannot persist grant; denied",
				zap.String("confirmID", confirmID), zap.String("kind", pending.kind),
				zap.String("decision", resp.Decision))
			resp = permission.ApprovalResponse{Decision: "deny"}
		}
		select {
		case pending.ch <- resp:
		default:
		}
	}
}
