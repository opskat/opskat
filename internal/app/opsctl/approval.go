package opsctl

import (
	"context"
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

// handleRequest 是 opsctl 本地 IPC 上每个请求的分发入口。ctx 在请求方断开或服务
// 停止时结束。
func (o *Opsctl) handleRequest(ctx context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
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

	// 扩展商店（opsctl ext search / install / update）
	if req.Type == approval.TypeExtStoreSearch {
		return o.handleExtStoreSearch(ctx, req)
	}
	if req.Type == approval.TypeExtStoreInstall {
		return o.handleExtStoreInstall(ctx, req)
	}

	return o.requestSingleApproval(req)
}

// startApprovalServer 启动 opsctl 审批 本地 IPC 服务
func (o *Opsctl) startApprovalServer() {
	srv := approval.NewServer(o.handleRequest, o.authToken)
	sockPath := approval.SocketPath(bootstrap.ResolvedDataDir())
	if err := srv.Start(sockPath); err != nil {
		logger.Ctx(o.ctx).Error("approval server failed to start", zap.String("socket", sockPath), zap.Error(err))
		return
	}
	o.approvalServer = srv
}

// requestSingleApproval 是 opsctl socket 上单条审批的入口：弹窗、等用户作答，"始终允许"
// 时按 opsctl 的 grant 语义（审批类型 + 命令串）落库。
func (o *Opsctl) requestSingleApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	parsed, reason := o.awaitSingleApproval(o.ctx, permission.ApprovalItem{
		Type: req.Type, AssetID: req.AssetID, AssetName: req.AssetName,
		Command: req.Command, Detail: req.Detail,
	}, req.SessionID)
	switch parsed.Decision {
	case permission.ApprovalAllow:
		return approval.ApprovalResponse{Approved: true}
	case permission.ApprovalAllowAll:
		if req.SessionID == "" {
			return approval.ApprovalResponse{Approved: false, Reason: "approval does not support a grant without a session"}
		}
		pattern, origin := grantPatternAndOrigin(req.Command, parsed.EditedItems)
		permission.SaveGrantPatternsForApproval(i18n.Ctx(o.ctx, o.lang.Lang()), req.SessionID, req.AssetID, req.AssetName, req.Type, pattern, origin)
		// 只回 Approved：ApproveGrant 在 opsctl 协议里是"整个会话已获批"（grant 审批），
		// CLI 见到它会把会话写成活动会话——单条命令的"始终允许"不是那个意思。
		return approval.ApprovalResponse{Approved: true}
	default:
		return approval.ApprovalResponse{Approved: false, Reason: reason}
	}
}

// opsctlAuditSource 是 opsctl 发起的审批与扩展工具调用落审计的来源列。
const opsctlAuditSource = "opsctl"

// awaitSingleApproval 在桌面端 "opsctl:approval" 弹窗里展示 item，等用户作答并校验。
// 它不落任何 grant："始终允许"授权什么由调用方决定——opsctl socket 按命令串落
// （requestSingleApproval），扩展工具闸门交给 HandleConfirm 按 (action, resource) 落
// （extToolConfirm）。拒绝时第二个返回值是原因。ctx 是发起这次审批的调用：它被取消
// 就不再等待，按拒绝返回，之后到达的作答找不到这条待决审批。
func (o *Opsctl) awaitSingleApproval(ctx context.Context, item permission.ApprovalItem, sessionID string) (permission.ParsedApprovalResponse, string) {
	confirmID := fmt.Sprintf("opsctl_%d", time.Now().UnixNano())
	kind := permission.ApprovalKindFor(item.Type, item.Command)
	log := logger.Ctx(o.ctx).With(
		zap.String("confirmID", confirmID),
		zap.String("approvalType", item.Type),
		zap.Int64("assetID", item.AssetID),
		zap.String("sessionID", sessionID),
	)
	log.Info("opsctl approval started")
	denied := permission.ParsedApprovalResponse{Decision: permission.ApprovalDeny}

	if o.window != nil {
		o.window.ActivateWindow()
	}

	expectedItems := []permission.ApprovalItem{item}
	ch := make(chan permission.ApprovalResponse, 1)
	// 先登记再发事件：响应一到就要找得到这条待决审批。
	o.pendingOpsctlApprovals.Store(confirmID, pendingOpsctlApproval{kind: kind, items: expectedItems, ch: ch})
	defer o.pendingOpsctlApprovals.Delete(confirmID)
	// 发往 Wails 的 command/detail 即原始主体，展示与执行逐字一致；action/resource/
	// resources/remember_pattern 只有扩展类型才有（check_policy 的分类；resources 是这次
	// 调用触及的全部资源，按扩展返回的原样），前端据此在命令上方展示，并让"记住"编辑
	// 实际落库的 <action>:<resource>。
	o.emit("opsctl:approval", map[string]any{
		"confirm_id":       confirmID,
		"kind":             kind,
		"type":             item.Type,
		"asset_id":         item.AssetID,
		"asset_name":       item.AssetName,
		"command":          item.Command,
		"detail":           item.Detail,
		"action":           item.Action,
		"resource":         item.Resource,
		"resources":        item.Resources,
		"remember_pattern": item.RememberPattern,
		"session_id":       sessionID,
	})

	select {
	case resp := <-ch:
		parsed, err := permission.ParseApprovalResponse(kind, resp, expectedItems)
		if err != nil {
			// Response payload is an IPC input and may contain credential-shaped text.
			// The scoped logger already carries confirmID/type/asset/session correlation.
			log.Warn("opsctl approval response invalid", zap.String("kind", kind))
			return denied, "invalid approval response"
		}
		log.Info("opsctl approval completed",
			zap.Bool("approved", parsed.Decision != permission.ApprovalDeny), zap.String("decision", resp.Decision))
		if parsed.Decision == permission.ApprovalDeny {
			return parsed, "user denied"
		}
		return parsed, ""
	case <-ctx.Done():
		log.Info("opsctl approval abandoned: the call was canceled")
		return denied, "call canceled"
	case <-o.ctx.Done():
		log.Error("opsctl approval failed", zap.Error(o.ctx.Err()))
		return denied, "app shutting down"
	case <-o.appCtx.Done():
		log.Error("opsctl approval failed", zap.Error(o.appCtx.Err()))
		return denied, "app shutting down"
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
// secrets 开头对象的常驻授权。它直接可测，不必经 requestSingleApproval 走一趟弹窗与落库。
func grantPatternAndOrigin(command string, edited []permission.ApprovalItem) (string, permission.GrantOrigin) {
	if len(edited) > 0 {
		return edited[0].Command, permission.GrantOriginUser
	}
	return command, permission.GrantOriginSystem
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
// 写入 grant_items——审批者看到的主体就是最终持久化的授权主体。扩展资产的条目例外：
// 审批者看到的是规则语法 `<action>[:<resource-glob>]`，落库的是它经同一编解码得出的
// ext:<policyType>:… 规则（extGrants 按请求下标给出），每条规则一行。
func grantItemsForPersistence(sessionID string, reqItems []approval.GrantItem, extGrants map[int]permission.ExtensionGrant) []*grant_entity.GrantItem {
	items := make([]*grant_entity.GrantItem, 0, len(reqItems))
	for i, item := range reqItems {
		toolName, commands := item.Type, []string{item.Command}
		if grant, ok := extGrants[i]; ok {
			toolName, commands = grant.Type, grant.Rules
		}
		for _, command := range commands {
			items = append(items, &grant_entity.GrantItem{
				GrantSessionID: sessionID,
				ItemIndex:      i,
				ToolName:       toolName,
				AssetID:        item.AssetID,
				AssetName:      item.AssetName,
				GroupID:        item.GroupID,
				GroupName:      item.GroupName,
				Command:        command,
				Detail:         item.Detail,
			})
		}
	}
	return items
}

// extensionGrantsFor 校验一次 grant 请求（或用户对它的编辑）里指向扩展资产的条目
// （spec 参数级策略 › 授权请求），按下标返回它们落库的规则。任何一条不合法就整个请求
// 拒绝：落一条永远匹配不上的 grant 再回报"已批准"，正是要防的缺陷。
func extensionGrantsFor(ctx context.Context, items []permission.ApprovalItem) (map[int]permission.ExtensionGrant, error) {
	grants := make(map[int]permission.ExtensionGrant)
	for i, item := range items {
		if item.AssetID == 0 {
			continue
		}
		grant, isExt, err := permission.ExtensionGrantForAsset(ctx, item.AssetID, item.Command)
		if err != nil {
			return nil, fmt.Errorf("grant item %d (%s): %w", i, item.AssetName, err)
		}
		if isExt {
			grants[i] = grant
		}
	}
	return grants, nil
}

// handleGrantApproval 处理批量计划审批
func (o *Opsctl) handleGrantApproval(req approval.ApprovalRequest) approval.ApprovalResponse {
	ctx := i18n.Ctx(o.ctx, o.lang.Lang())
	sessionID := req.SessionID

	expectedItems := make([]permission.ApprovalItem, 0, len(req.GrantItems))
	for _, item := range req.GrantItems {
		expectedItems = append(expectedItems, permission.ApprovalItem{
			Type: item.Type, AssetID: item.AssetID, AssetName: item.AssetName,
			GroupID: item.GroupID, GroupName: item.GroupName, Command: item.Command, Detail: item.Detail,
		})
	}
	extGrants, err := extensionGrantsFor(ctx, expectedItems)
	if err != nil {
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}
	for i, grant := range extGrants {
		// 扩展条目带扩展类型：弹窗里的编辑按同一规则语法校验（ParseApprovalResponse）。
		expectedItems[i].Type = grant.Type
	}

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

	// 授权事件发送原始 items；pending 保留原始 expectedItems 用于校验与执行。
	items := grantItemsForPersistence(sessionID, req.GrantItems, extGrants)
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

	o.emit("opsctl:grant-approval", map[string]any{
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
		var editGrants map[int]permission.ExtensionGrant
		if parseErr == nil {
			editGrants, parseErr = extensionGrantsFor(ctx, parsed.EditedItems)
		}
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
				lines, toolName := strings.Split(edit.Command, "\n"), "exec"
				if grant, ok := editGrants[i]; ok {
					lines, toolName = grant.Rules, grant.Type
				}
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					items = append(items, &grant_entity.GrantItem{
						GrantSessionID: sessionID,
						ItemIndex:      i,
						ToolName:       toolName,
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

// extToolGateResult is what gateExtToolCall hands back: the output and decision,
// plus the normalized command the unified exec handler actually ran —
// canonicalization can rewrite flag order/spelling, and the audit row should show
// what ran, not what was sent.
type extToolGateResult struct {
	output            string
	normalizedCommand string
	decision          aictx.CheckResult
}

// gateExtToolCall runs one extension-tool invocation through the exact same
// policy check / in-app approval / grant / audit pipeline AI's unified exec uses.
//
// NeedConfirm surfaces through the desktop's one "opsctl:approval" dialog
// (extToolConfirm).
//
// It returns the context it built (audit source/session/decision slot all
// installed on it) alongside the result: a caller's own WriteToolCall must use
// that returned context, not the one it passed in, or the audit row it writes
// carries none of what this function just annotated, the audit source included.
func (o *Opsctl) gateExtToolCall(ctx context.Context, sessionID string, assetID int64, command string) (context.Context, extToolGateResult, error) {
	ctx = aictx.WithAuditSource(ctx, opsctlAuditSource)
	ctx = aictx.WithSessionID(ctx, sessionID)
	checker := permission.NewCommandPolicyChecker(o.extToolConfirm(sessionID))
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

// extToolConfirm is the confirm step of the extension-tool gate: the item
// HandleConfirm built — for an extension type it carries the check_policy
// (action, resource) and the formatted request — goes to the desktop's one
// "opsctl:approval" dialog, and the user's answer goes back unchanged, edits
// included. Persisting an "always allow" is HandleConfirm's: it grants the
// classification, the only shape extension grant matching reads.
func (o *Opsctl) extToolConfirm(sessionID string) permission.CommandConfirmFunc {
	return func(ctx context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		parsed, _ := o.awaitSingleApproval(ctx, items[0], sessionID)
		switch parsed.Decision {
		case permission.ApprovalAllowAll:
			return permission.ApprovalResponse{Decision: "allowAll", EditedItems: parsed.EditedItems}
		case permission.ApprovalAllow:
			return permission.ApprovalResponse{Decision: "allow"}
		default:
			return permission.ApprovalResponse{Decision: "deny"}
		}
	}
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
	gateCtx, gateResult, err := o.gateExtToolCall(ctx, req.SessionID, req.AssetID, req.Command)
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
