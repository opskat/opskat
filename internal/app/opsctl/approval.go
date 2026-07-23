package opsctl

import (
	"context"
	"encoding/json"
	"fmt"
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

		return o.requestSingleApproval(req)
	}

	srv := approval.NewServer(handler, o.authToken)
	sockPath := approval.SocketPath(bootstrap.ResolvedDataDir())
	if err := srv.Start(sockPath); err != nil {
		logger.Ctx(o.ctx).Error("approval server failed to start", zap.String("socket", sockPath), zap.Error(err))
		return
	}
	o.approvalServer = srv
}

func (o *Opsctl) requestSingleApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	confirmID := fmt.Sprintf("opsctl_%d", time.Now().UnixNano())
	kind := singleApprovalKind(req.Type)
	log := logger.Ctx(o.ctx).With(
		zap.String("confirmID", confirmID),
		zap.String("approvalType", req.Type),
		zap.Int64("assetID", req.AssetID),
		zap.String("sessionID", req.SessionID),
	)
	log.Info("opsctl approval started")

	if o.window != nil {
		o.window.ActivateWindow()
	}

	wailsRuntime.EventsEmit(o.ctx, "opsctl:approval", map[string]any{
		"confirm_id": confirmID,
		"kind":       kind,
		"type":       req.Type,
		"asset_id":   req.AssetID,
		"asset_name": req.AssetName,
		"command":    req.Command,
		"detail":     req.Detail,
		"session_id": req.SessionID,
	})

	ch := make(chan permission.ApprovalResponse, 1)
	expectedItems := []permission.ApprovalItem{{
		Type: req.Type, AssetID: req.AssetID, AssetName: req.AssetName,
		Command: req.Command, Detail: req.Detail,
	}}
	o.pendingOpsctlApprovals.Store(confirmID, pendingOpsctlApproval{kind: kind, items: expectedItems, ch: ch})
	defer o.pendingOpsctlApprovals.Delete(confirmID)

	select {
	case resp := <-ch:
		parsed, err := permission.ParseApprovalResponse(kind, resp, expectedItems)
		if err != nil {
			log.Warn("opsctl approval response invalid", zap.String("kind", kind),
				zap.String("decision", resp.Decision), zap.Error(err))
			return approval.ApprovalResponse{Approved: false, Reason: "invalid approval response"}
		}
		switch parsed.Decision {
		case permission.ApprovalDeny:
			log.Info("opsctl approval completed", zap.Bool("approved", false), zap.String("decision", resp.Decision))
			return approval.ApprovalResponse{Approved: false, Reason: "user denied"}
		case permission.ApprovalAllow:
			log.Info("opsctl approval completed", zap.Bool("approved", true), zap.String("decision", resp.Decision))
			return approval.ApprovalResponse{Approved: true}
		case permission.ApprovalAllowAll:
			if req.SessionID == "" {
				return approval.ApprovalResponse{Approved: false, Reason: "approval does not support a grant without a session"}
			}
			pattern := req.Command
			if len(parsed.EditedItems) > 0 {
				pattern = parsed.EditedItems[0].Command
			}
			permission.SaveGrantPatternsForApproval(i18n.Ctx(o.ctx, o.lang.Lang()), req.SessionID, req.AssetID, req.AssetName, req.Type, pattern)
			log.Info("opsctl approval completed", zap.Bool("approved", true), zap.String("decision", resp.Decision))
			return approval.ApprovalResponse{Approved: true}
		default:
			return approval.ApprovalResponse{Approved: false, Reason: "unsupported approval decision"}
		}
	case <-o.ctx.Done():
		log.Error("opsctl approval failed", zap.Error(o.ctx.Err()))
		return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
	case <-o.appCtx.Done():
		log.Error("opsctl approval failed", zap.Error(o.appCtx.Err()))
		return approval.ApprovalResponse{Approved: false, Reason: "app shutting down"}
	}
}

func singleApprovalKind(approvalType string) string {
	switch approvalType {
	case permission.ApprovalTypeDelete:
		return permission.ApprovalKindDelete
	case "ext_tool":
		return permission.ApprovalKindExtension
	default:
		if permission.SupportsGrantApproval(approvalType) {
			return permission.ApprovalKindSingle
		}
		return permission.ApprovalKindOnce
	}
}

// startSSHPoolServer 启动 SSH 连接池 proxy 服务
func (o *Opsctl) startSSHPoolServer() {
	if o.proxyServer == nil {
		return
	}
	sockPath := sshpool.SocketPath(bootstrap.ResolvedDataDir())
	if err := o.proxyServer.Start(sockPath); err != nil {
		logger.Ctx(o.ctx).Error("ssh pool server failed to start", zap.String("socket", sockPath), zap.Error(err))
	}
}

// handleBatchApproval 处理批量执行审批（exec/sql/redis 混合）
func (o *Opsctl) handleBatchApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	confirmID := fmt.Sprintf("batch_%d", time.Now().UnixNano())

	items := make([]map[string]any, 0, len(req.BatchItems))
	expectedItems := make([]permission.ApprovalItem, 0, len(req.BatchItems))
	for _, item := range req.BatchItems {
		items = append(items, map[string]any{
			"type":       item.Type,
			"asset_id":   item.AssetID,
			"asset_name": item.AssetName,
			"command":    item.Command,
		})
		expectedItems = append(expectedItems, permission.ApprovalItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName, Command: item.Command,
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
	o.pendingOpsctlApprovals.Store(confirmID, pendingOpsctlApproval{kind: permission.ApprovalKindBatch, items: expectedItems, ch: ch})
	defer o.pendingOpsctlApprovals.Delete(confirmID)

	select {
	case resp := <-ch:
		parsed, err := permission.ParseApprovalResponse(permission.ApprovalKindBatch, resp, expectedItems)
		if err != nil || parsed.Decision != permission.ApprovalAllow {
			if err != nil {
				logger.Ctx(o.ctx).Warn("opsctl batch approval response invalid",
					zap.String("decision", resp.Decision), zap.Error(err))
			}
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
	expectedItems := make([]permission.ApprovalItem, 0, len(req.GrantItems))
	for _, item := range req.GrantItems {
		expectedItems = append(expectedItems, permission.ApprovalItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			GroupID: item.GroupID, GroupName: item.GroupName, Command: item.Command, Detail: item.Detail,
		})
	}
	o.pendingOpsctlApprovals.Store(sessionID, pendingOpsctlApproval{kind: permission.ApprovalKindGrant, items: expectedItems, ch: ch})
	defer o.pendingOpsctlApprovals.Delete(sessionID)

	select {
	case resp := <-ch:
		parsed, parseErr := permission.ParseApprovalResponse(permission.ApprovalKindGrant, resp, expectedItems)
		if parseErr != nil || parsed.Decision != permission.ApprovalAllow {
			if err := grant_repo.Grant().UpdateSessionStatus(ctx, sessionID, grant_entity.GrantStatusRejected); err != nil {
				logger.Default().Error("update grant session status to rejected", zap.Error(err))
			}
			return approval.ApprovalResponse{Approved: false, Reason: "user denied", SessionID: sessionID}
		}
		if err := grant_repo.Grant().UpdateSessionStatus(ctx, sessionID, grant_entity.GrantStatusApproved); err != nil {
			logger.Default().Error("update grant session status to approved", zap.Error(err))
		}
		if len(parsed.EditedItems) > 0 {
			var items []*grant_entity.GrantItem
			for i, edit := range parsed.EditedItems {
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

	ctx := i18n.Ctx(o.ctx, o.lang.Lang())
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		item := items[0]
		resp := o.requestSingleApproval(approval.ApprovalRequest{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			Command: item.Command, Detail: item.Detail,
		})
		if resp.Approved {
			return permission.ApprovalResponse{Decision: "allow"}
		}
		return permission.ApprovalResponse{Decision: "deny"}
	})
	ctx = permission.WithPolicyChecker(ctx, checker)
	result, err := o.extExecutor.ExecuteExtTool(ctx, req.Extension, req.Tool, args)
	if err != nil {
		return approval.ApprovalResponse{ToolError: fmt.Sprintf("call tool %s/%s: %v", req.Extension, req.Tool, err)}
	}

	return approval.ApprovalResponse{Approved: true, ToolResult: string(result)}
}

// RespondOpsctlApproval 前端响应 opsctl 审批请求（统一入口）
func (o *Opsctl) RespondOpsctlApproval(confirmID string, resp permission.ApprovalResponse) {
	if v, ok := o.pendingOpsctlApprovals.Load(confirmID); ok {
		pending := v.(pendingOpsctlApproval)
		if _, err := permission.ParseApprovalResponse(pending.kind, resp, pending.items); err != nil {
			logger.Ctx(o.ctx).Warn("invalid opsctl approval response denied",
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
