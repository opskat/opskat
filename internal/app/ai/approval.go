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

		wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
			Type:      "approval_request",
			Kind:      kind,
			Items:     items,
			ConfirmID: confirmID,
		})

		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store(confirmID, pendingAIApproval{kind: kind, items: items, ch: ch})
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

		wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
			Type:        "approval_request",
			Kind:        permission.ApprovalKindGrant,
			Items:       items,
			ConfirmID:   confirmID,
			Description: reason,
			SessionID:   fmt.Sprintf("conv_%d", convID),
		})

		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store(confirmID, pendingAIApproval{kind: permission.ApprovalKindGrant, items: items, ch: ch})
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
		wailsRuntime.EventsEmit(a.ctx, eventName, runner.StreamEvent{
			Type:      "approval_request",
			Kind:      permission.ApprovalKindLocalTool,
			ConfirmID: confirmID,
			ToolName:  req.ToolName,
			Items:     approvalItems,
			Patterns:  req.DefaultPatterns,
		})

		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store(confirmID, pendingAIApproval{kind: permission.ApprovalKindLocalTool, items: approvalItems, ch: ch})
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
		if _, err := permission.ParseApprovalResponse(pending.kind, resp, pending.items); err != nil {
			logger.Ctx(a.ctx).Warn("invalid AI approval response denied",
				zap.String("confirmID", confirmID), zap.String("kind", pending.kind),
				zap.String("decision", resp.Decision), zap.Error(err))
			resp = permission.ApprovalResponse{Decision: "deny"}
		}
		select {
		case pending.ch <- resp:
		default:
		}
	}
}
