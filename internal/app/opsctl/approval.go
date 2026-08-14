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

	expectedItems := []permission.ApprovalItem{{
		Type: req.Type, AssetID: req.AssetID, AssetName: req.AssetName,
		Command: req.Command, Detail: req.Detail,
	}}
	// 发往 Wails 的 command/detail 即原始主体，展示与执行逐字一致。
	wailsRuntime.EventsEmit(o.ctx, "opsctl:approval", map[string]any{
		"confirm_id": confirmID,
		"kind":       kind,
		"type":       req.Type,
		"asset_id":   req.AssetID,
		"asset_name": req.AssetName,
		"command":    expectedItems[0].Command,
		"detail":     expectedItems[0].Detail,
		"session_id": req.SessionID,
	})

	ch := make(chan permission.ApprovalResponse, 1)
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
			pattern, origin := grantPatternAndOrigin(req.Command, parsed.EditedItems)
			permission.SaveGrantPatternsForApproval(i18n.Ctx(o.ctx, o.lang.Lang()), req.SessionID, req.AssetID, req.AssetName, req.Type, pattern, origin)
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

// grantPatternAndOrigin 决定这次 allowAll 要把哪条串落成常驻授权、以及它算谁写的。
//
// 来源必须跟着 pattern 一起走：用户在弹窗里改过的就是他手写的授权范围（他写的通配
// 就是他要的范围，归一化不该收窄），没改过的是系统交上来的主体——exec 的规范 DSL、
// cp 的 ApprovalSubject——要按 decision D20 / D21 收窄（见 permission.GrantOrigin）。
//
// 单独成函数是因为这条判断**没有编译期守卫**：origin 是必填参数，所以"忘了传"编译不过，
// 但"传错一个"照样编译通过，而它的后果是安全性的——把 System 写成 User，
// `opsctl cp 's3-prod:/mybucket/secrets*' ./` 点一次"始终允许"落下的就是一条读遍所有
// secrets 开头对象的常驻授权。requestSingleApproval 本身跑不进测试（wailsRuntime.EventsEmit
// 拿到非 wails context 会直接终止进程），所以判断留在函数里就等于没有任何锁。
func grantPatternAndOrigin(command string, edited []permission.ApprovalItem) (string, permission.GrantOrigin) {
	if len(edited) > 0 {
		return edited[0].Command, permission.GrantOriginUser
	}
	return command, permission.GrantOriginSystem
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

	expectedItems := make([]permission.ApprovalItem, 0, len(req.BatchItems))
	for _, item := range req.BatchItems {
		expectedItems = append(expectedItems, permission.ApprovalItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName, Command: item.Command, Detail: item.Detail,
		})
	}
	// 批量事件发送原始 items，展示与执行逐字一致。
	items := make([]map[string]any, 0, len(expectedItems))
	for i := range expectedItems {
		items = append(items, map[string]any{
			"type":       expectedItems[i].Type,
			"asset_id":   expectedItems[i].AssetID,
			"asset_name": expectedItems[i].AssetName,
			"command":    expectedItems[i].Command,
			"detail":     expectedItems[i].Detail,
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

// grantItemsForPersistence 构造可在审批前保存的 grant items。原始 command/detail 原样
// 写入 grant_items——审批者看到的主体就是最终持久化的授权主体。
func grantItemsForPersistence(sessionID string, reqItems []approval.GrantItem) []*grant_entity.GrantItem {
	items := make([]*grant_entity.GrantItem, 0, len(reqItems))
	for i, item := range reqItems {
		items = append(items, &grant_entity.GrantItem{
			GrantSessionID: sessionID,
			ItemIndex:      i,
			ToolName:       item.Type,
			AssetID:        item.AssetID,
			AssetName:      item.AssetName,
			GroupID:        item.GroupID,
			GroupName:      item.GroupName,
			Command:        item.Command,
			Detail:         item.Detail,
		})
	}
	return items
}

// handleGrantApproval 处理批量计划审批
func (o *Opsctl) handleGrantApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	ctx := i18n.Ctx(o.ctx, o.lang.Lang())
	sessionID := req.SessionID

	description := req.Description
	session := &grant_entity.GrantSession{
		ID:          sessionID,
		Description: description,
		Status:      grant_entity.GrantStatusPending,
		Createtime:  time.Now().Unix(),
	}
	if err := grant_repo.Grant().CreateSession(ctx, session); err != nil {
		if _, getErr := grant_repo.Grant().GetSession(ctx, sessionID); getErr != nil {
			return approval.ApprovalResponse{Approved: false, Reason: "failed to create grant session"}
		}
	}

	expectedItems := make([]permission.ApprovalItem, 0, len(req.GrantItems))
	for _, item := range req.GrantItems {
		expectedItems = append(expectedItems, permission.ApprovalItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			GroupID: item.GroupID, GroupName: item.GroupName, Command: item.Command, Detail: item.Detail,
		})
	}
	// 授权事件发送原始 items；pending 保留原始 expectedItems 用于校验与执行。
	items := grantItemsForPersistence(sessionID, req.GrantItems)
	if err := grant_repo.Grant().CreateItems(ctx, items); err != nil {
		return approval.ApprovalResponse{Approved: false, Reason: "failed to create grant items"}
	}
	eventItems := make([]map[string]any, 0, len(expectedItems))
	for i := range expectedItems {
		eventItems = append(eventItems, map[string]any{
			"type":       expectedItems[i].Type,
			"asset_id":   expectedItems[i].AssetID,
			"asset_name": expectedItems[i].AssetName,
			"group_id":   expectedItems[i].GroupID,
			"group_name": expectedItems[i].GroupName,
			"command":    expectedItems[i].Command,
			"detail":     expectedItems[i].Detail,
		})
	}

	if o.window != nil {
		o.window.ActivateWindow()
	}

	wailsRuntime.EventsEmit(o.ctx, "opsctl:grant-approval", map[string]any{
		"session_id":  sessionID,
		"description": description,
		"items":       eventItems,
	})

	ch := make(chan permission.ApprovalResponse, 1)
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
	// 直接执行通道：executor 返回的 bytes 与 error 原样交给调用者，不做内容改写
	// （spec Decision 4 / Direct execution and history）。
	result, err := o.extExecutor.ExecuteExtTool(ctx, req.Extension, req.Tool, args)
	if err != nil {
		return approval.ApprovalResponse{ToolError: err.Error()}
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
