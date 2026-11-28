package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/mcp"
)

// mcpApprover answers every human decision of one opsctl mcp session. It asks in the
// calling MCP client's own prompt when the client can render one (mcp.Ask), otherwise in
// the running desktop app's approval dialog; with neither, the request is denied.
// Answers are the approval responses the built-in AI and opsctl produce, so session
// grants and audit go through the shared permission code either way.
//
// Permanent rules are never offered: they are written only after a human confirms them
// on a terminal or in the desktop app (opsctl policy, the desktop dialog), not from
// inside an agent's tool call. SSH MFA stays desktop-only (withDesktopOnlyMFA): a form
// prompt must not be used to collect a secret.
type mcpApprover struct {
	sessionID string
}

// mcpChoice is one answer a client prompt offers; decision is the ApprovalResponse value
// it maps to.
type mcpChoice struct {
	decision string
	en, zh   string
}

var (
	choiceAllow        = mcpChoice{decision: "allow", en: "Allow once", zh: "允许一次"}
	choiceAllowSession = mcpChoice{decision: "allowAll", en: "Allow for this session", zh: "本会话允许"}
	choiceGrant        = mcpChoice{decision: "allow", en: "Grant for this session", zh: "授予本会话"}
	choiceDeny         = mcpChoice{decision: "deny", en: "Deny", zh: "拒绝"}
)

// maxPromptDetailRunes keeps a long detail (a file preview, a whole script) from
// burying the command in a client prompt; the desktop dialog shows it in full.
const maxPromptDetailRunes = 600

func (a mcpApprover) checker() *permission.CommandPolicyChecker {
	c := permission.NewCommandPolicyChecker(a.confirm)
	c.SetGrantRequestFunc(a.grant)
	return c
}

func (a mcpApprover) localToolGate() *tool.LocalToolGate {
	return tool.NewLocalToolGate(a.confirmLocalTool)
}

// confirm answers a CommandPolicyChecker confirmation. "Allow for this session" is
// offered exactly where the approval kind can persist a grant
// (permission.KindAllowsSessionGrant); the checker then saves it under this session.
func (a mcpApprover) confirm(ctx context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
	choices := []mcpChoice{choiceAllow}
	offersSession := permission.KindAllowsSessionGrant(kind)
	if offersSession {
		choices = append(choices, choiceAllowSession)
	}
	choices = append(choices, choiceDeny)
	if decision, asked := askMCPClient(ctx, approvalPromptMessage(ctx, items, offersSession), choices); asked {
		return permission.ApprovalResponse{Decision: decision}
	}
	return desktopMCPDecision(ctx, desktopApprovalRequest(a.sessionID, kind, items))
}

// grant answers request_permission. Approved patterns are persisted the way the
// desktop AI grant dialog persists them (internal/app/ai): as this session's grants,
// normalized as system-proposed subjects since the prompt offers no editing.
func (a mcpApprover) grant(ctx context.Context, items []permission.ApprovalItem, reason string) (bool, []string) {
	decision, asked := askMCPClient(ctx, grantPromptMessage(ctx, items, reason), []mcpChoice{choiceGrant, choiceDeny})
	if !asked {
		return a.desktopGrant(ctx, items, reason)
	}
	if decision != choiceGrant.decision {
		return false, nil
	}
	patterns := make([]string, 0, len(items))
	for _, it := range items {
		permission.SaveGrantPatternsForApproval(ctx, a.sessionID, it.AssetID, it.AssetName, it.Type, it.Command, permission.GrantOriginSystem)
		patterns = append(patterns, it.Command)
	}
	return true, patterns
}

func (a mcpApprover) confirmLocalTool(ctx context.Context, req tool.LocalToolApprovalRequest) permission.ApprovalResponse {
	if decision, asked := askMCPClient(ctx, localToolPromptMessage(ctx, req), []mcpChoice{choiceAllow, choiceAllowSession, choiceDeny}); asked {
		return permission.ApprovalResponse{Decision: decision}
	}
	return desktopMCPDecision(ctx, approval.ApprovalRequest{
		Type: req.ToolName, Kind: permission.ApprovalKindOnce, Command: req.Command, Detail: req.Detail, SessionID: a.sessionID,
	})
}

// askMCPClient asks the client to pick one of choices. asked is false when the client
// cannot show prompts. Anything but an accepted, offered choice is a denial.
func askMCPClient(ctx context.Context, message string, choices []mcpChoice) (decision string, asked bool) {
	options := make([]map[string]any, 0, len(choices))
	for _, c := range choices {
		options = append(options, map[string]any{"const": c.decision, "title": policy.PolicyMsg(ctx, c.en, c.zh)})
	}
	res, ok := mcp.Ask(ctx, &mcpsdk.ElicitParams{
		Message: message,
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"decision": map[string]any{
					"type":  "string",
					"title": policy.PolicyMsg(ctx, "Decision", "决定"),
					"oneOf": options,
				},
			},
			"required": []string{"decision"},
		},
	})
	if !ok {
		return "", false
	}
	if res == nil || res.Action != "accept" {
		return choiceDeny.decision, true
	}
	picked, _ := res.Content["decision"].(string)
	for _, c := range choices {
		if c.decision == picked {
			return picked, true
		}
	}
	logger.Ctx(ctx).Warn("mcp client prompt answered with a choice that was not offered; denying", zap.String("decision", picked))
	return choiceDeny.decision, true
}

// The prompt messages lead with what is being decided — "<asset> [<type>] <command>" —
// because clients show only the first two lines of a long message until it is
// expanded, and already title the prompt with the server's name. The model review,
// when there is one, is the next most important line; supporting detail follows.

// approvalPromptMessage renders a checker confirmation. offersSession says whether the
// prompt offers "allow for this session"; when it does, the message names exactly what
// that choice saves (permission.ApprovalRuleSubjects — the subjects the terminal prompt
// lists and the checker persists), which is not always the command shown.
func approvalPromptMessage(ctx context.Context, items []permission.ApprovalItem, offersSession bool) string {
	var b strings.Builder
	if len(items) > 1 {
		b.WriteString(policy.PolicyFmt(ctx, "%d operations need approval", "%d 项操作待审批", len(items)))
	}
	for _, it := range items {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s [%s] %s", promptTarget(it), it.Type, it.Command)
		if it.Review != nil {
			b.WriteString("\n" + permission.ReviewSummary(ctx, it.Review))
		}
		if offersSession {
			subjects := permission.ApprovalRuleSubjects(ctx, it.Type, it)
			writePromptLine(ctx, &b, "Allow for this session grants", "本会话允许将授权",
				strings.Join(subjects, policy.PolicyMsg(ctx, "; ", "；")))
		}
		if it.Action != "" {
			writePromptLine(ctx, &b, "Action", "动作", it.Action+" "+strings.Join(it.Resources, ", "))
		}
		if it.Detail != it.Command {
			writePromptLine(ctx, &b, "Detail", "详情", truncateRunes(it.Detail, maxPromptDetailRunes))
		}
	}
	return b.String()
}

func grantPromptMessage(ctx context.Context, items []permission.ApprovalItem, reason string) string {
	var b strings.Builder
	b.WriteString(policy.PolicyMsg(ctx, "Grant for the rest of this session:", "授权本会话后续操作："))
	for _, it := range items {
		fmt.Fprintf(&b, "\n%s: %s", promptTarget(it), it.Command)
	}
	writePromptLine(ctx, &b, "Reason", "原因", reason)
	return b.String()
}

func localToolPromptMessage(ctx context.Context, req tool.LocalToolApprovalRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", policy.PolicyMsg(ctx, "this computer", "本机"), req.ToolName, req.Command)
	writePromptLine(ctx, &b, "Detail", "详情", truncateRunes(req.Detail, maxPromptDetailRunes))
	return b.String()
}

func writePromptLine(ctx context.Context, b *strings.Builder, en, zh, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "\n%s%s", policy.PolicyMsg(ctx, en+": ", zh+"："), value)
}

func promptTarget(it permission.ApprovalItem) string {
	switch {
	case it.AssetName != "":
		return it.AssetName
	case it.AssetID > 0:
		return fmt.Sprintf("#%d", it.AssetID)
	case it.GroupName != "":
		return it.GroupName
	case it.GroupID > 0:
		return fmt.Sprintf("group #%d", it.GroupID)
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// desktopApprovalRequest is the desktop dialog's form of a checker confirmation. The
// desktop persists session grants itself (keyed by SessionID), so a desktop allow
// comes back as a plain "allow".
func desktopApprovalRequest(sessionID, kind string, items []permission.ApprovalItem) approval.ApprovalRequest {
	var req approval.ApprovalRequest
	if kind == permission.ApprovalKindBatch {
		req.Type = "batch"
		for _, it := range items {
			req.BatchItems = append(req.BatchItems, permission.BatchItemOfApprovalItem(it))
		}
	} else {
		req = permission.RequestOfApprovalItem(items[0]) // non-batch kinds always carry exactly one item (CommandPolicyChecker)
	}
	req.Kind, req.SessionID = kind, sessionID
	return req
}

// desktopGrant shows request_permission in the desktop grant dialog, which persists the
// approved (possibly edited) grants itself.
func (a mcpApprover) desktopGrant(ctx context.Context, items []permission.ApprovalItem, reason string) (bool, []string) {
	req := approval.ApprovalRequest{Type: "grant", SessionID: a.sessionID, Description: reason}
	for _, it := range items {
		req.GrantItems = append(req.GrantItems, approval.GrantItem{
			Type: it.Type, AssetID: it.AssetID, AssetName: it.AssetName,
			GroupID: it.GroupID, GroupName: it.GroupName, Command: it.Command, Detail: it.Detail,
		})
	}
	resp, err := requestDesktopContext(ctx, req)
	if err != nil || !resp.Approved {
		logDesktopMCPRefusal(ctx, req, err)
		return false, nil
	}
	final := resp.EditedItems
	if len(final) == 0 {
		final = req.GrantItems
	}
	patterns := make([]string, 0, len(final))
	for _, it := range final {
		patterns = append(patterns, it.Command)
	}
	return true, patterns
}

// desktopMCPDecision sends one approval request to the desktop and maps the answer onto
// the checker's decision; anything but an explicit approval is a denial.
func desktopMCPDecision(ctx context.Context, req approval.ApprovalRequest) permission.ApprovalResponse {
	resp, err := requestDesktopContext(ctx, req)
	if err != nil || !resp.Approved {
		logDesktopMCPRefusal(ctx, req, err)
		return permission.ApprovalResponse{Decision: "deny"}
	}
	return permission.ApprovalResponse{Decision: "allow"}
}

func logDesktopMCPRefusal(ctx context.Context, req approval.ApprovalRequest, err error) {
	if err != nil {
		logger.Ctx(ctx).Warn("mcp desktop approval unavailable; denying",
			zap.String("approvalType", req.Type), zap.String("sessionID", req.SessionID), zap.Error(err))
	}
}
