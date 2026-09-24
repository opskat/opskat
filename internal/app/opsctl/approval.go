package opsctl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// startApprovalServer 启动 opsctl 审批 本地 IPC 服务
func (o *Opsctl) startApprovalServer() {
	handler := func(ctx context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
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

		// SSH MFA 挑战：opsctl 不可交互时请桌面端代答；请求方断开即关闭对话框
		if req.Type == "mfa" {
			return o.mfa.challenge(ctx, req)
		}

		// 扩展工具执行
		if req.Type == "ext_tool" {
			return o.handleExtToolExec(req)
		}

		// 扩展开发安装（opsctl ext dev）
		if req.Type == "ext_dev_install" {
			return o.handleExtDevInstall(req)
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
	kind := permission.ApprovalKindFor(req.Type, req.Command)
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
			// Response payload is an IPC input and may contain credential-shaped text.
			// The scoped logger already carries confirmID/type/asset/session correlation.
			log.Warn("opsctl approval response invalid", zap.String("kind", kind))
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
			// ApproveGrant / EditedItems let gateExtToolCall's confirmFunc tell "allow"
			// and "allow all" apart: the generic grant just saved above is keyed by
			// req.Type + raw command text, which extension policy matching
			// (MatchExtensionGrant) never reads — only gateExtToolCall's caller knows
			// the checker it installed is HandleConfirm, whose own extension-aware
			// branch persists the (action, resource) grant that actually gets matched.
			// Every other caller of requestSingleApproval already ignores these two
			// fields, so setting them here does not change their behavior.
			return approval.ApprovalResponse{
				Approved:     true,
				ApproveGrant: true,
				EditedItems:  approvalGrantItemsFrom(parsed.EditedItems),
			}
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

// approvalGrantItemsFrom converts a parsed approval's edited items back into the
// IPC-shaped approval.GrantItem the ApprovalResponse.EditedItems field carries —
// the inverse of the []approval.GrantItem → []permission.ApprovalItem conversion
// ParseApprovalResponse does on the way in. gateExtToolCall's confirmFunc needs
// them in this shape to hand back to HandleConfirm, which re-classifies edited
// items through the extension's own ClassifyFunc rather than trusting Action/
// Resource a caller could have forged.
func approvalGrantItemsFrom(items []permission.ApprovalItem) []approval.GrantItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]approval.GrantItem, 0, len(items))
	for _, item := range items {
		out = append(out, approval.GrantItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			GroupID: item.GroupID, GroupName: item.GroupName,
			Command: item.Command, Detail: item.Detail,
		})
	}
	return out
}

// convertApprovalGrantItems is approvalGrantItemsFrom's inverse: gateExtToolCall's
// confirmFunc receives requestSingleApproval's IPC-shaped EditedItems and must hand
// permission.ApprovalItem back to the checker, which is what HandleConfirm and its
// own ParseApprovalResponse call expect.
func convertApprovalGrantItems(items []approval.GrantItem) []permission.ApprovalItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]permission.ApprovalItem, 0, len(items))
	for _, item := range items {
		out = append(out, permission.ApprovalItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			GroupID: item.GroupID, GroupName: item.GroupName,
			Command: item.Command, Detail: item.Detail,
		})
	}
	return out
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
					zap.String("confirmID", confirmID))
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

// extToolGateResult is what gateExtToolCall hands back to either caller: exactly
// the three things that exist regardless of who initiated the call (output,
// decision, error), plus the normalized command the unified exec handler actually
// ran — canonicalization can rewrite flag order/spelling, and a caller's audit row
// should show what ran, not what was sent.
type extToolGateResult struct {
	output            string
	normalizedCommand string
	decision          aictx.CheckResult
}

// gateExtToolCall runs one extension-tool invocation through the exact same
// policy check / in-app approval / grant / audit pipeline AI's unified exec uses,
// regardless of who is asking: opsctl's socket bridge (handleExtToolExec) and an
// extension's own frontend page (RunPageToolCall) differ only in audit source and
// grant-session identity — the gate itself does not know which one called.
//
// NeedConfirm always surfaces through requestSingleApproval, the desktop's one
// "opsctl:approval" dialog: a page call gets the identical in-app approval UI an
// opsctl or AI-initiated call gets, not a second one.
//
// It returns the context it built (audit source/session/decision slot all
// installed on it) alongside the result: a caller's own WriteToolCall must use
// that returned context, not the one it passed in, or the audit row it writes
// carries none of what this function just annotated — including the audit
// source, which is the entire reason a page call and an opsctl call end up
// distinguishable in audit_logs at all.
func (o *Opsctl) gateExtToolCall(ctx context.Context, source, sessionID string, assetID int64, command string) (context.Context, extToolGateResult, error) {
	ctx = aictx.WithAuditSource(ctx, source)
	ctx = aictx.WithSessionID(ctx, sessionID)
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
		item := items[0]
		resp := o.requestSingleApproval(approval.ApprovalRequest{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			Command: item.Command, Detail: item.Detail, SessionID: sessionID,
		})
		if !resp.Approved {
			return permission.ApprovalResponse{Decision: "deny"}
		}
		if resp.ApproveGrant {
			// The user picked "remember" in the dialog. requestSingleApproval already
			// persisted its own grant for this — a plain req.Type + raw-command-text
			// pattern, right for the built-in types its *other* callers register, but
			// never read by extension policy matching (MatchExtensionGrant only ever
			// looks for `ext:<type>:<action>:<resource>`-shaped patterns). Every asset
			// type this gate runs against is an extension type (only extension assets
			// reach ExecuteExtTool/RunPageToolCall), so forwarding "allowAll" here lets
			// HandleConfirm's own extension-aware branch persist the one grant that
			// actually gets matched on the next call — the earlier generic one is
			// harmless, inert leftover, not a competing source of truth.
			return permission.ApprovalResponse{Decision: "allowAll", EditedItems: convertApprovalGrantItems(resp.EditedItems)}
		}
		return permission.ApprovalResponse{Decision: "allow"}
	})
	ctx = permission.WithPolicyChecker(ctx, checker)

	// 审计行由**做出决策的进程**写：策略判定、审批结果与规范化命令都只在这里存在，
	// 调用方侧只知道最终的结果字符串。command slot 让统一 exec 的规范化形式落库，
	// 与 AI 会话那条路径一致。
	var decision aictx.CheckResult
	ctx = aictx.WithCheckResultSlot(ctx, &decision)
	normalizedCommand := command
	ctx = aictx.WithAuditCommandSlot(ctx, &normalizedCommand)

	result, err := o.extExecutor.ExecuteExtTool(ctx, assetID, command)
	return ctx, extToolGateResult{output: result, normalizedCommand: normalizedCommand, decision: decision}, err
}

// handleExtToolExec 处理 opsctl 对扩展资产的委托执行请求。
//
// 桌面端在这里跑的是与 AI 会话完全相同的统一 exec handler：策略检查、审批弹窗、
// "始终允许"落 grant、审计，全部由 gateExtToolCall 那条路径提供。本函数只负责把 opsctl
// 的 socket 请求翻译成一次进程内调用，并按 opsctl 自己的语义（拒绝 = socket 错误）
// 把结果译回 approval.ApprovalResponse。
func (o *Opsctl) handleExtToolExec(req approval.ApprovalRequest) approval.ApprovalResponse {
	if o.extExecutor == nil {
		return approval.ApprovalResponse{ToolError: "extension system not initialized"}
	}

	ctx := i18n.Ctx(o.ctx, o.lang.Lang())
	gateCtx, gateResult, err := o.gateExtToolCall(ctx, "opsctl", req.SessionID, req.AssetID, req.Command)
	decision := gateResult.decision
	extAuditWriter.WriteToolCall(gateCtx, audit.ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: fmt.Sprintf(`{"asset_id":%d,"command":%q}`, req.AssetID, req.Command),
		Command:  gateResult.normalizedCommand,
		Result:   gateResult.output,
		Error:    err,
		Decision: &decision,
	})
	if err != nil {
		return approval.ApprovalResponse{ToolError: err.Error()}
	}
	// 统一 exec 把拒绝当普通文本结果交回（那是给模型看的，模型据此调整），但对
	// opsctl 而言命令没有执行——决策槽里的 Deny 是唯一可靠的信号，按错误回传，
	// 客户端才会与内置类型一样 exit 1 + stderr，而不是把拒绝文本当输出打到 stdout。
	if decision.Decision != aictx.Allow {
		return approval.ApprovalResponse{ToolError: gateResult.output}
	}
	return approval.ApprovalResponse{Approved: true, ToolResult: gateResult.output}
}

// RunPageToolCall runs one call an extension's own frontend page makes against
// the asset it was opened for, through the exact gate handleExtToolExec runs
// opsctl's delegated commands through: same policy check, same in-app "opsctl:
// approval" dialog, same grant persistence, same audit pipeline — only the audit
// source differs, so a page call and an AI/opsctl exec on the same asset show up
// in the same audit trail shape and honor the same "always allow" grants.
//
// invocationID is not the grant session id: it is the frontend's per-call
// correlation token (CallExtensionAction's convention, mirrored by
// CallExtensionTool), minted fresh for every call so a *future* call can cancel
// *this one* in flight — the opposite lifetime a grant session needs. A grant
// keyed by it would only ever cover the single call that requested it, which
// fails the very point of "always allow": pageGrantSessionID gives every call
// against the same asset in this desktop run the same session instead, so
// "remember" on one call is honored by the next one, not just replayed by itself.
//
// A Deny (or a NeedConfirm the user rejects) comes back as an error: the page
// gets a rejection it must handle, not a text result meant for a model to read.
func (o *Opsctl) RunPageToolCall(ctx context.Context, invocationID string, assetID int64, command string) (string, error) {
	if o.extExecutor == nil {
		return "", fmt.Errorf("extension system not initialized")
	}

	gateCtx, gateResult, err := o.gateExtToolCall(ctx, "extension_page", o.pageGrantSessionID(assetID), assetID, command)
	decision := gateResult.decision
	extAuditWriter.WriteToolCall(gateCtx, audit.ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: fmt.Sprintf(`{"asset_id":%d,"command":%q}`, assetID, command),
		Command:  gateResult.normalizedCommand,
		Result:   gateResult.output,
		Error:    err,
		Decision: &decision,
	})
	if err != nil {
		return "", err
	}
	if decision.Decision != aictx.Allow {
		return "", errors.New(gateResult.output)
	}
	return gateResult.output, nil
}

// pageGrantSessionID names the grant session a page call's "always allow" is
// persisted under and matched against. A page has no session concept of its own
// the way an AI conversation or an opsctl CLI invocation does, so it is derived
// from the asset and this desktop run: "always allow" set from one open page (or
// tab, or a page reopened later) is honored by a call from another in the same
// run, and — like every other grant session, bounded by its conversation or its
// opsctl session — does not outlive it as a permanent rule the asset's policy
// card never shows.
func (o *Opsctl) pageGrantSessionID(assetID int64) string {
	return fmt.Sprintf("ext_page_%s_asset_%d", o.pageRunID, assetID)
}

// extAuditWriter 与 opsctl CLI 侧用的是同一个默认写入器实现，两条路径落同一组列语义。
var extAuditWriter audit.AuditWriter = audit.NewDefaultAuditWriter()

// RespondOpsctlApproval 前端响应 opsctl 审批请求（统一入口）
func (o *Opsctl) RespondOpsctlApproval(confirmID string, resp permission.ApprovalResponse) {
	if v, ok := o.pendingOpsctlApprovals.Load(confirmID); ok {
		pending := v.(pendingOpsctlApproval)
		if _, err := permission.ParseApprovalResponse(pending.kind, resp, pending.items); err != nil {
			// Response payload is an IPC input and may contain credential-shaped text.
			// Keep only correlation fields; the static message already identifies validation failure.
			logger.Ctx(o.ctx).Warn("invalid opsctl approval response denied",
				zap.String("confirmID", confirmID), zap.String("kind", pending.kind))
			resp = permission.ApprovalResponse{Decision: "deny"}
		}
		select {
		case pending.ch <- resp:
		default:
		}
	}
}
