package opsctl

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// startApprovalServer 启动 opsctl 审批 Unix socket 服务
func (o *Opsctl) startApprovalServer() {
	handler := func(req approval.ApprovalRequest) approval.ApprovalResponse {
		// 数据变更通知：opsctl 通知前端刷新
		if req.Type == "notify" {
			wailsRuntime.EventsEmit(o.ctx, "data:changed", map[string]any{
				"resource": req.Detail,
			})
			return approval.ApprovalResponse{Approved: true}
		}

		// 授权审批
		if req.Type == "grant" {
			return o.handleGrantApproval(req)
		}

		// 批量执行审批
		if req.Type == "batch" {
			return o.handleBatchApproval(req)
		}

		// 扩展工具执行
		if req.Type == "ext_tool" {
			return o.handleExtToolExec(req)
		}

		// 单条审批
		confirmID := fmt.Sprintf("opsctl_%d", time.Now().UnixNano())

		if o.window != nil {
			o.window.ActivateWindow()
		}

		wailsRuntime.EventsEmit(o.ctx, "opsctl:approval", map[string]any{
			"confirm_id": confirmID,
			"type":       req.Type,
			"asset_id":   req.AssetID,
			"asset_name": req.AssetName,
			"command":    req.Command,
			"detail":     req.Detail,
			"session_id": req.SessionID,
		})

		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store(confirmID, ch)
		defer o.pendingOpsctlApprovals.Delete(confirmID)

		select {
		case resp := <-ch:
			if resp.Decision == "deny" {
				return approval.ApprovalResponse{Approved: false, Reason: "user denied"}
			}
			if resp.Decision == "allowAll" && req.SessionID != "" {
				pattern := req.Command
				if len(resp.EditedItems) > 0 {
					pattern = resp.EditedItems[0].Command
				}
				permission.SaveGrantPatternsForApproval(i18n.Ctx(o.ctx, o.lang.Lang()), req.SessionID, req.AssetID, req.AssetName, req.Type, pattern)
			}
			return approval.ApprovalResponse{Approved: true}
		case <-o.ctx.Done():
			return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
		case <-o.appCtx.Done():
			return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
		}
	}

	srv := approval.NewServer(handler, o.authToken)
	sockPath := approval.SocketPath(bootstrap.AppDataDir())
	if err := srv.Start(sockPath); err != nil {
		log.Printf("Approval server failed to start: %v", err)
		return
	}
	o.approvalServer = srv
}

// startSSHPoolServer 启动 SSH 连接池 proxy 服务
func (o *Opsctl) startSSHPoolServer() {
	if o.proxyServer == nil {
		return
	}
	sockPath := sshpool.SocketPath(bootstrap.AppDataDir())
	if err := o.proxyServer.Start(sockPath); err != nil {
		log.Printf("SSH pool server failed to start: %v", err)
	}
}

// handleBatchApproval 处理批量执行审批（exec/sql/redis 混合）
func (o *Opsctl) handleBatchApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	confirmID := fmt.Sprintf("batch_%d", time.Now().UnixNano())

	items := make([]map[string]any, 0, len(req.BatchItems))
	for _, item := range req.BatchItems {
		items = append(items, map[string]any{
			"type":       item.Type,
			"asset_id":   item.AssetID,
			"asset_name": item.AssetName,
			"command":    item.Command,
		})
	}

	if o.window != nil {
		o.window.ActivateWindow()
	}

	wailsRuntime.EventsEmit(o.ctx, "opsctl:batch-approval", map[string]any{
		"confirm_id": confirmID,
		"session_id": req.SessionID,
		"items":      items,
	})

	ch := make(chan permission.ApprovalResponse, 1)
	o.pendingOpsctlApprovals.Store(confirmID, ch)
	defer o.pendingOpsctlApprovals.Delete(confirmID)

	select {
	case resp := <-ch:
		if resp.Decision == "deny" {
			return approval.ApprovalResponse{Approved: false, Reason: "user denied"}
		}
		return approval.ApprovalResponse{Approved: true}
	case <-o.ctx.Done():
		return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
	case <-o.appCtx.Done():
		return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
	}
}

// handleGrantApproval 处理批量计划审批
func (o *Opsctl) handleGrantApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	ctx := i18n.Ctx(o.ctx, o.lang.Lang())
	sessionID := req.SessionID

	session := &grant_entity.GrantSession{
		ID:          sessionID,
		Description: req.Description,
		Status:      grant_entity.GrantStatusPending,
		Createtime:  time.Now().Unix(),
	}
	if err := grant_repo.Grant().CreateSession(ctx, session); err != nil {
		if _, getErr := grant_repo.Grant().GetSession(ctx, sessionID); getErr != nil {
			return approval.ApprovalResponse{Approved: false, Reason: "failed to create grant session"}
		}
	}

	var items []*grant_entity.GrantItem
	for i, pi := range req.GrantItems {
		items = append(items, &grant_entity.GrantItem{
			GrantSessionID: sessionID,
			ItemIndex:      i,
			ToolName:       pi.Type,
			AssetID:        pi.AssetID,
			AssetName:      pi.AssetName,
			GroupID:        pi.GroupID,
			GroupName:      pi.GroupName,
			Command:        pi.Command,
			Detail:         pi.Detail,
		})
	}
	if err := grant_repo.Grant().CreateItems(ctx, items); err != nil {
		return approval.ApprovalResponse{Approved: false, Reason: "failed to create grant items"}
	}

	eventItems := make([]map[string]any, 0, len(req.GrantItems))
	for _, pi := range req.GrantItems {
		eventItems = append(eventItems, map[string]any{
			"type":       pi.Type,
			"asset_id":   pi.AssetID,
			"asset_name": pi.AssetName,
			"group_id":   pi.GroupID,
			"group_name": pi.GroupName,
			"command":    pi.Command,
			"detail":     pi.Detail,
		})
	}

	if o.window != nil {
		o.window.ActivateWindow()
	}

	wailsRuntime.EventsEmit(o.ctx, "opsctl:grant-approval", map[string]any{
		"session_id":  sessionID,
		"description": req.Description,
		"items":       eventItems,
	})

	ch := make(chan permission.ApprovalResponse, 1)
	o.pendingOpsctlApprovals.Store(sessionID, ch)
	defer o.pendingOpsctlApprovals.Delete(sessionID)

	select {
	case resp := <-ch:
		if resp.Decision == "deny" {
			if err := grant_repo.Grant().UpdateSessionStatus(ctx, sessionID, grant_entity.GrantStatusRejected); err != nil {
				logger.Default().Error("update grant session status to rejected", zap.Error(err))
			}
			return approval.ApprovalResponse{Approved: false, Reason: "user denied", SessionID: sessionID}
		}
		if err := grant_repo.Grant().UpdateSessionStatus(ctx, sessionID, grant_entity.GrantStatusApproved); err != nil {
			logger.Default().Error("update grant session status to approved", zap.Error(err))
		}
		if len(resp.EditedItems) > 0 {
			var items []*grant_entity.GrantItem
			for i, edit := range resp.EditedItems {
				lines := strings.Split(edit.Command, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					items = append(items, &grant_entity.GrantItem{
						GrantSessionID: sessionID,
						ItemIndex:      i,
						ToolName:       "exec",
						AssetID:        edit.AssetID,
						AssetName:      edit.AssetName,
						GroupID:        edit.GroupID,
						GroupName:      edit.GroupName,
						Command:        line,
					})
				}
			}
			if len(items) > 0 {
				if err := grant_repo.Grant().UpdateItems(ctx, sessionID, items); err != nil {
					logger.Default().Error("update grant items", zap.Error(err))
				}
			}
		}
		finalResp := approval.ApprovalResponse{Approved: true, SessionID: sessionID}
		if finalItems, err := grant_repo.Grant().ListItems(ctx, sessionID); err == nil {
			for _, item := range finalItems {
				finalResp.EditedItems = append(finalResp.EditedItems, approval.GrantItem{
					Type:      item.ToolName,
					AssetID:   item.AssetID,
					AssetName: item.AssetName,
					GroupID:   item.GroupID,
					GroupName: item.GroupName,
					Command:   item.Command,
					Detail:    item.Detail,
				})
			}
		}
		return finalResp
	case <-o.ctx.Done():
		if err := grant_repo.Grant().UpdateSessionStatus(ctx, sessionID, grant_entity.GrantStatusRejected); err != nil {
			logger.Default().Error("update grant session status to rejected on shutdown", zap.Error(err))
		}
		return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
	case <-o.appCtx.Done():
		if err := grant_repo.Grant().UpdateSessionStatus(ctx, sessionID, grant_entity.GrantStatusRejected); err != nil {
			logger.Default().Error("update grant session status to rejected on shutdown", zap.Error(err))
		}
		return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
	}
}

// handleExtToolExec 处理 opsctl ext exec 的委托执行请求
func (o *Opsctl) handleExtToolExec(req approval.ApprovalRequest) approval.ApprovalResponse {
	if o.extExecutor == nil {
		return approval.ApprovalResponse{ToolError: "extension system not initialized"}
	}

	args := req.ToolArgs
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	result, err := o.extExecutor.ExecuteExtTool(i18n.Ctx(o.ctx, o.lang.Lang()), req.Extension, req.Tool, args)
	if err != nil {
		return approval.ApprovalResponse{ToolError: fmt.Sprintf("call tool %s/%s: %v", req.Extension, req.Tool, err)}
	}

	return approval.ApprovalResponse{Approved: true, ToolResult: string(result)}
}

// RespondOpsctlApproval 前端响应 opsctl 审批请求（统一入口）
func (o *Opsctl) RespondOpsctlApproval(confirmID string, resp permission.ApprovalResponse) {
	if v, ok := o.pendingOpsctlApprovals.Load(confirmID); ok {
		ch := v.(chan permission.ApprovalResponse)
		select {
		case ch <- resp:
		default:
		}
	}
}
