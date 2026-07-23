package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
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
	commandSlot := ""
	ctx := aictx.WithCheckResultSlot(c.Context(), slot)
	ctx = aictx.WithAuditCommandSlot(ctx, &commandSlot)
	c.WithContext(ctx)

	assetID, assetName, command := resolveAssetForAudit(c.Context(), c.ToolName, c.Input)

	c.Next()
	if commandSlot != "" {
		command = commandSlot
	}
	// Deny 是工具调用的失败终态，即使具体 handler 按 agent 协议把拒绝说明作为
	// 普通文本返回。统一在 middleware 边界标记，事件、UI 与审计由同一结构化语义驱动。
	if slot.Decision == aictx.Deny && c.Output != nil {
		c.Output.IsError = true
	}

	argsJSON, err := json.Marshal(c.Input)
	if err != nil {
		logger.Default().Warn("audit middleware marshal input", zap.Error(err))
	}

	result, errVal := extractAuditResult(c.Output)
	if assetID == 0 && errVal == nil && audit.ShouldResolveAssetFromResult(c.ToolName) {
		assetID, assetName = resolveResultAssetForAudit(c.Context(), result)
	}

	info := audit.ToolCallInfo{
		ToolName:  c.ToolName,
		ArgsJSON:  string(argsJSON),
		Result:    result,
		Error:     errVal,
		Decision:  slot,
		AssetID:   assetID,
		AssetName: assetName,
		Command:   command,
	}
	auditCtx := detachAuditContext(c.Context())
	go auditWriter.WriteToolCall(auditCtx, info)
}

func resolveResultAssetForAudit(ctx context.Context, result string) (int64, string) {
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(result), &created); err != nil || created.ID <= 0 {
		return 0, ""
	}
	asset, err := assetref.Resolve(ctx, strconv.FormatInt(created.ID, 10))
	if err != nil {
		return 0, ""
	}
	return asset.ID, asset.Name
}

// resolveAssetForAudit resolves the tool's args["asset"] identifier (numeric id or
// name string — the convention shared by exec/help, and any single-asset tool)
// before c.Next() runs the tool, so the audit row keeps correct attribution
// even if the tool itself changes the asset's status: asset_repo filters by
// status=Active, so resolving afterward would silently lose the name for the
// delete_asset tool — exactly the operation where the name matters most.
//
// It lives here rather than in package audit: audit can't import assetref/permission
// without an import cycle (permission already imports audit, to audit its own policy
// checks), but runner already depends on both, so it resolves once here and threads
// the result through audit.ToolCallInfo instead.
//
// It also computes the command's canonical form for tools that registered via
// audit.RegisterCanonicalizingTool (currently only "exec") on asset types that in turn
// register a permission.CanonicalizeFunc (k8s injects --context/--namespace; etcd
// round-trips through ParseCommand+FormatCommand to normalize case, compound-op
// spelling, and flag order) — matching what the permission check and approval dialog
// already saw, so the audit row doesn't show the model's raw string while the approval
// showed the effective one.
//
// The audit.RegisterCanonicalizingTool gate matters because args["asset"]+args["command"]
// is not unique to "exec": ext_exec shares the exact same argument shape but its command
// is an extension's own invocation syntax, never the target asset type's exec DSL.
// Running a k8s asset's CanonicalizeFunc on an ext_exec command doesn't error (it just
// tokenizes the string as if it were kubectl args) — it silently rewrites the audit row
// into a command that was never approved nor executed. Gating by an allow-list (not a
// toolName == "exec" branch here) follows the same "register, don't switch" pattern as
// audit.RegisterGroupScopedTool.
//
// Best-effort throughout: any resolution failure (missing/ambiguous/unknown asset,
// no canonicalize hook, canonicalize error) just leaves the corresponding return
// value at its zero value / the raw command, and the caller falls back to its
// existing args["asset_id"]/ExtractCommandForAudit path — which also covers every
// tool that doesn't use the asset-string convention at all (the pre-existing 14).
func resolveAssetForAudit(ctx context.Context, toolName string, input map[string]any) (assetID int64, assetName, command string) {
	ref := aictx.ArgString(input, "asset")
	if ref == "" {
		return 0, "", ""
	}
	asset, err := assetref.Resolve(ctx, ref)
	if err != nil {
		return 0, "", ""
	}

	raw := aictx.ArgString(input, "command")
	if raw == "" {
		return asset.ID, asset.Name, ""
	}
	command = raw
	if audit.ShouldCanonicalizeCommand(toolName) {
		if canonicalize, ok := permission.CanonicalizeFor(asset.Type); ok {
			if canonical, cerr := canonicalize(asset, raw); cerr == nil {
				command = canonical
			}
		}
	}
	return asset.ID, asset.Name, command
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
