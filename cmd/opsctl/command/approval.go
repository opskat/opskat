package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 结构化拒绝契约（spec Structured refusal contract）：不可交互且桌面端不可达时，
// stderr 首行是两个固定标记之一（恒定英文 ASCII），进程退出码为 3；1 仍是一般错误。
const (
	needsAuthorizationMarker = "NEEDS AUTHORIZATION" // 有主体：规则能救，给出可照抄的 policy allow 命令
	needsTTYMarker           = "NEEDS TTY"           // 无主体：只能由人在终端里做
	refusalExitCode          = 3
)

// ApprovalResult 审批结果，包含决策来源信息（用于审计）
type ApprovalResult struct {
	Decision       aictx.Decision // Allow | Deny
	DecisionSource string         // ai.Source* 常量
	MatchedPattern string         // 匹配的规则或模式
	SessionID      string         // 会话 ID
}

// ToCheckResult 转换为 CheckResult（供 AuditWriter 使用）
func (ar ApprovalResult) ToCheckResult() *aictx.CheckResult {
	return &aictx.CheckResult{
		Decision:       ar.Decision,
		DecisionSource: ar.DecisionSource,
		MatchedPattern: ar.MatchedPattern,
	}
}

// structuredRefusal 是“不可交互且桌面端不可达”的拒绝错误：Error() 的首行是固定
// 标记，正文是给人/调用方的指引。调用方经 writeApprovalFailure 把它映射成退出码 3，
// 并保持 stderr 首行是裸标记（不加 "Error: " 前缀）。
type structuredRefusal struct {
	marker string
	body   string
}

func (e *structuredRefusal) Error() string {
	return e.marker + "\n" + e.body
}

// writeApprovalFailure 按契约上报审批路径的错误：结构化拒绝以裸标记作为 stderr
// 首行（机器可读）并返回退出码 3；其余错误保持普通的 "Error: " 前缀与退出码 1。
func writeApprovalFailure(w io.Writer, err error) int {
	var refusal *structuredRefusal
	if errors.As(err, &refusal) {
		fmt.Fprintln(w, err) //nolint:errcheck // 终端呈现尽力而为
		return refusalExitCode
	}
	fmt.Fprintf(w, "Error: %v\n", err) //nolint:errcheck // 终端呈现尽力而为
	return 1
}

// originCommandKeyType 是原命令原文的 context 槽。ApprovalRequest 没有这个字段而
// Detail 不保证是命令（create asset 放的是去密 JSON），NEEDS TTY 要转述给人的
// “原命令原文”只能由 CLI 边界在调用 requireApproval 前挂上来。
type originCommandKeyType struct{}

func withOriginCommand(ctx context.Context, cmd string) context.Context {
	return context.WithValue(ctx, originCommandKeyType{}, cmd)
}

func originCommandFromCtx(ctx context.Context) string {
	cmd, _ := ctx.Value(originCommandKeyType{}).(string)
	return cmd
}

// requireApproval 检查命令策略 → DB Grant 匹配 → 选择审批人（spec Approver selection）：
// 可交互（stdin 与 stderr 双 TTY）走终端提示、不联系桌面端；不可交互且 approval.sock
// 可达走桌面弹窗（保持现状，含 stale socket 判定）；否则结构化拒绝（退出码 3 + 固定标记）。
func requireApproval(ctx context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
	// Stage 1: Auto-create session if none exists
	if req.SessionID == "" {
		id := uuid.New().String()
		if err := writeActiveSession(id); err != nil {
			logger.Default().Warn("write active session", zap.String("sessionID", id), zap.Error(err))
		}
		req.SessionID = id
	}

	// Stage 2: 统一权限检查（策略 + DB Grant）— 与 AI 的 exec 工具共用 CheckPermission
	var policyHints []string
	if req.AssetID > 0 && req.Command != "" {
		// 注入 sessionID 到 context，供 matchGrantPatterns 使用
		permCtx := aictx.WithSessionID(ctx, req.SessionID)
		checkType := req.CheckType
		if checkType == "" {
			checkType = req.Type
		}
		permResult := permission.CheckPermission(permCtx, checkType, req.AssetID, req.Command)

		switch permResult.Decision {
		case aictx.Allow:
			return ApprovalResult{
				Decision:       aictx.Allow,
				DecisionSource: permResult.DecisionSource,
				MatchedPattern: permResult.MatchedPattern,
				SessionID:      req.SessionID,
			}, nil
		case aictx.Deny:
			return ApprovalResult{
				Decision:       aictx.Deny,
				DecisionSource: permResult.DecisionSource,
				MatchedPattern: permResult.MatchedPattern,
				SessionID:      req.SessionID,
			}, fmt.Errorf("command denied by policy: %s", permResult.Message)
		default: // NeedConfirm → fall through to approver selection
			policyHints = permResult.HintRules
		}
	}

	// Stage 3: 选择审批人。可交互时不发生拨号。
	dataDir := bootstrap.ResolvedDataDir()
	sockPath := approval.SocketPath(dataDir)

	switch chooseApprover(isInteractive(stdinIsTerminal(), stderrIsTerminal()), func() error {
		return dialApprovalSocket(sockPath)
	}) {
	case approverTerminal:
		in, out := terminalApprovalStreams()
		return runTTYApproval(ctx, req, in, out)

	case approverDesktop:
		authToken, err := bootstrap.ReadAuthToken(dataDir)
		if err != nil {
			logger.Default().Warn("read auth token", zap.Error(err))
		}

		resp, err := approval.RequestApprovalWithToken(sockPath, authToken, req)
		if err != nil {
			// 拨号在探测与请求之间失败：与不可达同一契约，结构化拒绝。
			return refuseApproval(ctx, req, policyHints)
		}
		if !resp.Approved {
			reason := resp.Reason
			if reason == "" {
				reason = "denied"
			}
			return ApprovalResult{
				Decision:       aictx.Deny,
				DecisionSource: aictx.SourceUserDeny,
				SessionID:      req.SessionID,
			}, fmt.Errorf("operation denied: %s", reason)
		}

		// If the desktop app approved the entire session, persist it locally
		if resp.ApproveGrant && req.SessionID != "" {
			if err := writeActiveSession(req.SessionID); err != nil {
				logger.Default().Warn("write active session", zap.String("sessionID", req.SessionID), zap.Error(err))
			}
		}

		return ApprovalResult{
			Decision:       aictx.Allow,
			DecisionSource: aictx.SourceUserAllow,
			SessionID:      req.SessionID,
		}, nil

	default:
		return refuseApproval(ctx, req, policyHints)
	}
}

// refusalSubject 是结构化拒绝里的一条可授权主体：某个资产上、某个审批类型下，
// 经 NormalizeGrantPatterns 归一化之后的 pattern 集合。
type refusalSubject struct {
	assetName    string
	assetID      int64
	approvalType string
	patterns     []string
}

// assetIdentity 渲染人辨认资产的三元组 "name (ID n, type t)"；name 为空时以 ID 顶上。
func assetIdentity(name string, id int64, typ string) string {
	who := name
	if who == "" {
		who = fmt.Sprintf("ID %d", id)
	}
	return fmt.Sprintf("%s (ID %d, type %s)", who, id, typ)
}

// refuseApproval 构造结构化拒绝：分支依据是这次操作有没有一个规则能匹配的主体
// （决策 17）——AssetID 与 Command 双全（与 Stage 2 的策略检查门槛同一判据）给
// NEEDS AUTHORIZATION，否则 create/update/delete 一类给 NEEDS TTY。它是真实的拒绝
// 决策，照常以 Deny + SourcePolicyDeny 落审计，不是参数错误。
func refuseApproval(ctx context.Context, req approval.ApprovalRequest, hints []string) (ApprovalResult, error) {
	var refusal *structuredRefusal
	var matched string
	if req.AssetID > 0 && req.Command != "" {
		// face 取 CheckType（方向化：cp 的 cp:read/cp:write），回落 req.Type——照抄命令
		// 与归一化主体都得按它给，折叠成 "cp" 会丢方向。
		face := ttyApprovalFace(req)
		patterns := normalizedApprovalSubjects(face, req.Command)
		matched = strings.Join(patterns, ", ")
		refusal = &structuredRefusal{
			marker: needsAuthorizationMarker,
			body: formatNeedsAuthorization(ctx, []refusalSubject{{
				assetName:    req.AssetName,
				assetID:      req.AssetID,
				approvalType: face,
				patterns:     patterns,
			}}, hints),
		}
	} else {
		cmd := originCommandFromCtx(ctx)
		if cmd == "" {
			cmd = strings.TrimSpace(req.Detail)
		}
		refusal = &structuredRefusal{marker: needsTTYMarker, body: formatNeedsTTY(ctx, req, cmd)}
	}
	logger.Ctx(ctx).Warn("opsctl approval refused: no interactive terminal and desktop app unreachable",
		zap.String("approvalType", req.Type),
		zap.Int64("assetID", req.AssetID),
		zap.String("sessionID", req.SessionID),
		zap.String("marker", refusal.marker))
	return ApprovalResult{
		Decision:       aictx.Deny,
		DecisionSource: aictx.SourcePolicyDeny,
		MatchedPattern: matched,
		SessionID:      req.SessionID,
	}, refusal
}

// refuseBatchApproval 是批量审批（opsctl batch / 多源 cp）的结构化拒绝：条条有主体，
// 恒为 NEEDS AUTHORIZATION，逐条给出可照抄的 policy allow 命令。
func refuseBatchApproval(items []approval.BatchItem, session string) (ApprovalResult, error) {
	ctx := envPolicyLangCtx()
	entries := make([]refusalSubject, 0, len(items))
	for _, item := range items {
		entries = append(entries, refusalSubject{
			assetName:    item.AssetName,
			assetID:      item.AssetID,
			approvalType: item.Type,
			patterns:     normalizedApprovalSubjects(item.Type, item.Command),
		})
	}
	err := &structuredRefusal{
		marker: needsAuthorizationMarker,
		body:   formatNeedsAuthorization(ctx, entries, nil),
	}
	logger.Ctx(ctx).Warn("opsctl batch approval refused: no interactive terminal and desktop app unreachable",
		zap.Int("items", len(items)),
		zap.String("sessionID", session),
		zap.String("marker", needsAuthorizationMarker))
	return ApprovalResult{
		Decision:       aictx.Deny,
		DecisionSource: aictx.SourcePolicyDeny,
		SessionID:      session,
	}, err
}

// formatNeedsAuthorization 构造 NEEDS AUTHORIZATION 的正文：资产名/ID/类型、被拒的
// 归一化主体、当前生效 allow 规则提示（HintRules，与旧离线拒绝同源）、以及人应照抄
// 执行的 opsctl policy allow 命令原文（已 shell 转义、可直接粘贴）。授权后由调用方重试。
func formatNeedsAuthorization(ctx context.Context, entries []refusalSubject, hints []string) string {
	var sb strings.Builder
	sb.WriteString(policy.PolicyMsg(ctx,
		"approval needed: stdin/stderr are not both terminals and the desktop app is unreachable",
		"需要授权：当前无交互终端，且桌面端不可达"))
	sb.WriteString("\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s %s\n", policy.PolicyMsg(ctx, "Asset:", "资产："),
			assetIdentity(e.assetName, e.assetID, e.approvalType))
		fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx, "Subject:", "主体："))
		for _, p := range e.patterns {
			fmt.Fprintf(&sb, "  - %s\n", p)
		}
	}
	if len(hints) > 0 {
		fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
			"Currently allowed by policy for this asset:",
			"该资产当前生效的 allow 规则："))
		for _, h := range hints {
			fmt.Fprintf(&sb, "  - %s\n", h)
		}
	}
	fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
		"Authorize by running this yourself, then retry:",
		"请自行执行以下命令授权，然后重试："))
	for _, e := range entries {
		for _, p := range e.patterns {
			fmt.Fprintf(&sb, "  %s\n", policyAllowCommand(e.assetID, e.approvalType, p))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatNeedsTTY 构造 NEEDS TTY 的正文：说明这类操作无主体、任何规则都不能预授权，
// 给出人应自行执行的原命令原文，明确不建议重试，且不给 policy allow 建议——那条
// 建议对它们永远无效。
func formatNeedsTTY(ctx context.Context, req approval.ApprovalRequest, originCmd string) string {
	var sb strings.Builder
	sb.WriteString(policy.PolicyMsg(ctx,
		"this operation cannot be pre-authorized: it has no subject any rule could match, and neither an interactive terminal nor the desktop app is available to approve it now",
		"该操作无法被预先授权：它没有可被规则匹配的主体，且当前既无交互终端、桌面端也不可达"))
	sb.WriteString("\n")
	if req.AssetName != "" || req.AssetID > 0 {
		fmt.Fprintf(&sb, "%s %s\n", policy.PolicyMsg(ctx, "Asset:", "资产："),
			assetIdentity(req.AssetName, req.AssetID, req.Type))
	}
	if originCmd != "" {
		fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
			"Run this yourself in a terminal (do not retry it from here):",
			"请在终端里自行执行以下命令（不要从这里重试）："))
		fmt.Fprintf(&sb, "  %s\n", originCmd)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// policyAllowCommand 渲染人应照抄执行的 opsctl policy allow 命令原文（shell 已转义、
// 恒定英文 ASCII、可直接粘贴），语法对齐 policy.go 落地的 CLI：pattern 是 "--" 之后
// 的位置参数；资产目标可省略 --type（形状由资产自身类型决定）。cp 面不是资产类型，
// 走 --type 会被资产目标的类型断言拒收，方向前缀必须放进 pattern——"cp:<dir>:<glob>"
// 本就是 CommandPolicy 里方向化 cp 规则的落库形态。
func policyAllowCommand(assetID int64, face, pattern string) string {
	if face == permission.GrantToolCpRead || face == permission.GrantToolCpWrite {
		return fmt.Sprintf("opsctl policy allow %d -- %s", assetID, shellQuote(face+":"+pattern))
	}
	return fmt.Sprintf("opsctl policy allow %d -- %s", assetID, shellQuote(pattern))
}

// shellQuote 按 POSIX 单引号规则包裹 s（内嵌的单引号用闭合-转义-重开的方式转义），
// 使渲染出的命令行可以原样粘贴进 shell。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
