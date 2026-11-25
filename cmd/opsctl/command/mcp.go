package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/mcp"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/audit_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/policy_rule_svc"
)

const mcpApprovalSecretEnv = "OPSKAT_MCP_APPROVAL_SECRET"

// mcpStdio holds the streams used by cmdMCP; overridden in tests.
var mcpStdio = func() (io.Reader, io.Writer) { return os.Stdin, os.Stderr }

// mcpStdout holds the JSON-RPC output stream used by cmdMCP; overridden in tests.
var mcpStdout = func() io.Writer { return os.Stdout }

func cmdMCP(ctx context.Context, args []string, session string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	approvalModeRaw := fs.String("approval-mode", string(mcp.ApprovalModeDesktop), "Approval handling mode: desktop or external")
	toolsetRaw := fs.String("toolset", string(mcp.ToolsetCore), "Exposed toolset: core or full")
	approvalTTL := fs.Duration("approval-ttl", 30*time.Minute, "Pending approval expiration duration in external mode")
	fs.Usage = printMCPUsage

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectExtraArgs(fs.Args()) {
		return 1
	}

	mode, err := mcp.ParseApprovalMode(*approvalModeRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	toolset, err := mcp.ParseToolset(*toolsetRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if *approvalTTL <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --approval-ttl must be positive")
		return 1
	}

	if session == "" {
		session = uuid.New().String()
		if err := writeActiveSession(session); err != nil {
			logger.Ctx(ctx).Warn("write active session for mcp", zap.String("sessionID", session), zap.Error(err))
		}
	}

	var approver *mcp.ExternalApprover
	var checker *permission.CommandPolicyChecker

	if mode == mcp.ApprovalModeExternal {
		secret := os.Getenv(mcpApprovalSecretEnv)
		approver, err = mcp.NewExternalApprover(mcp.ExternalApproverConfig{
			Secret:              secret,
			TTL:                 *approvalTTL,
			PermanentRuleWriter: writeMCPPermanentRules,
			GrantPersister:      newMCPGrantPersister(session),
			AssetNameLookup:     lookupMCPAssetName,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v (set %s)\n", err, mcpApprovalSecretEnv)
			return 1
		}
		checker = permission.NewCommandPolicyChecker(approver.ConfirmFunc)
		checker.SetGrantRequestFunc(approver.GrantRequestFunc)
	} else {
		checker = newDesktopMCPChecker(session)
	}

	docGate := tool.NewDocGate()
	tools := wrapMCPTools(mcp.FilterTools(tool.Tools(), toolset))
	extraTools := []mcp.ExtraTool{newMCPListAuditTool()}

	srv := mcp.NewServer(mcp.ServerConfig{
		ServerName:    "opsctl",
		ServerVersion: configs.Version,
		Tools:         tools,
		ExtraTools:    extraTools,
		Approver:      approver,
		AuditWriter:   opsctlAuditWriter,
		SessionID:     session,
		AuditSource:   "opsctl",
		PrepareCtx: func(callCtx context.Context) context.Context {
			callCtx = aictx.WithConversationID(callCtx, 1)
			callCtx = tool.WithDocGate(callCtx, docGate)
			callCtx = permission.WithPolicyChecker(callCtx, checker)
			if approver != nil {
				callCtx = credential_resolver.WithMFA(callCtx, approver.NewMFACaller)
				callCtx = credential_resolver.WithConnectionPathMFA(callCtx, approver.NewMFACaller)
			} else {
				callCtx = withMFA(withConnectionPathMFA(callCtx))
			}
			return callCtx
		},
		OnMutate: func(toolName string) {
			if refreshesDesktopUI(toolName) {
				notifyDesktopAssetChanged()
			}
		},
	})

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	in, _ := mcpStdio()
	out := mcpStdout()
	if err := srv.Serve(serveCtx, in, out); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func printMCPUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl mcp [flags]

Run a stdio MCP (Model Context Protocol) server exposing OpsKat tools.

Flags:
  --approval-mode <desktop|external>
        How NeedConfirm operations are approved (default: desktop).
        desktop  - Delegate to the running OpsKat desktop approval dialog.
        external - Pause and return structured needs_approval / needs_mfa
                   payloads for an external host adapter (requires
                   OPSKAT_MCP_APPROVAL_SECRET).
  --toolset <core|full>
        Which tools to expose via tools/list (default: core).
        core - list_assets, get_asset, help, exec, batch_exec, cp,
               request_permission, list_audit (+ get_task in external mode).
        full - All core tools plus list_groups, get_group, list_credentials,
               get_credential, put_asset, put_group, delete_asset,
               delete_group, get_asset_secret.
  --approval-ttl <duration>
        Expiration timeout for pending external approvals (default: 30m).
`)
}

// wrapMCPTools wraps exec and batch_exec so extension-provided asset types and DB
// drivers are activated on demand inside the long-lived MCP process, matching cmdExec.
func wrapMCPTools(tools []agent.Tool) []agent.Tool {
	out := make([]agent.Tool, 0, len(tools))
	for _, t := range tools {
		switch t.Name() {
		case "exec":
			out = append(out, &extensionActivatingTool{inner: t})
		case "batch_exec":
			out = append(out, &batchExtensionActivatingTool{inner: t})
		default:
			out = append(out, t)
		}
	}
	return out
}

func activateExtensionsForRef(ctx context.Context, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return nil
	}
	asset, err := resolveAsset(ctx, ref)
	if err != nil || asset == nil {
		return nil
	}
	if err := refusedExtensionError(ctx, asset); err != nil {
		return err
	}
	extName, isExt, err := extensionOwnerOf(asset.Type)
	if err != nil {
		return err
	}
	if isExt {
		if err := activateExtensionFn(ctx, extName); err != nil {
			return err
		}
	}
	return activateDriverExtension(ctx, asset, activateExtensionFn)
}

type extensionActivatingTool struct {
	inner agent.Tool
}

func (e *extensionActivatingTool) Name() string        { return e.inner.Name() }
func (e *extensionActivatingTool) Description() string { return e.inner.Description() }
func (e *extensionActivatingTool) Schema() agent.Schema { return e.inner.Schema() }

func (e *extensionActivatingTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	if err := activateExtensionsForRef(ctx, aictx.ArgString(input, "asset")); err != nil {
		return nil, err
	}
	return e.inner.Call(ctx, input)
}

type batchExtensionActivatingTool struct {
	inner agent.Tool
}

func (b *batchExtensionActivatingTool) Name() string        { return b.inner.Name() }
func (b *batchExtensionActivatingTool) Description() string { return b.inner.Description() }
func (b *batchExtensionActivatingTool) Schema() agent.Schema { return b.inner.Schema() }

func (b *batchExtensionActivatingTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	if raw := input["commands"]; raw != nil {
		var items []struct {
			Asset string `json:"asset"`
		}
		if bts, err := json.Marshal(raw); err == nil {
			if err := json.Unmarshal(bts, &items); err != nil {
				var s string
				if json.Unmarshal(bts, &s) == nil {
					_ = json.Unmarshal([]byte(s), &items)
				}
			}
			for _, item := range items {
				if err := activateExtensionsForRef(ctx, item.Asset); err != nil {
					return nil, err
				}
			}
		}
	}
	return b.inner.Call(ctx, input)
}

func lookupMCPAssetName(ctx context.Context, assetID int64) string {
	svc := asset_svc.Asset()
	if svc == nil {
		return ""
	}
	asset, err := svc.Get(ctx, assetID)
	if err != nil || asset == nil {
		return ""
	}
	return asset.Name
}

func writeMCPPermanentRules(ctx context.Context, items []permission.ApprovalItem, edited []permission.ApprovalItem) error {
	if len(items) == 0 {
		return errors.New("no approval items to persist")
	}
	svc := policy_rule_svc.PolicyRule()
	if svc == nil {
		return errors.New("policy rule service unavailable")
	}

	targets := make([]policy_rule_svc.Target, 0, len(items))
	for i, item := range items {
		if item.AssetID <= 0 {
			return errors.New("permanent allow rule requires a target asset")
		}
		asset, err := svc.FindAsset(ctx, item.AssetID)
		if err != nil {
			return err
		}
		canonical := asset.Type
		if item.Type == permission.GrantToolCpRead || item.Type == permission.GrantToolCpWrite {
			canonical = item.Type
		}
		var patterns []string
		if i < len(edited) && strings.TrimSpace(edited[i].Command) != "" {
			patterns = []string{strings.TrimSpace(edited[i].Command)}
		} else {
			patterns = permission.ApprovalRuleSubjects(ctx, item.Type, item)
		}
		if len(patterns) == 0 {
			return fmt.Errorf("no rule subject available for asset %q", asset.Name)
		}
		targets = append(targets, policy_rule_svc.Target{
			Asset:     asset,
			Canonical: canonical,
			Patterns:  patterns,
		})
	}
	return svc.AppendRules(ctx, permission.RuleAllow, targets)
}

func newMCPGrantPersister(sessionID string) mcp.GrantPersister {
	return func(ctx context.Context, items []permission.ApprovalItem, edited []permission.ApprovalItem) ([]string, error) {
		if sessionID == "" {
			return nil, errors.New("session ID is required to persist grant")
		}
		origin := permission.GrantOriginSystem
		source := items
		if len(edited) > 0 {
			origin = permission.GrantOriginUser
			source = edited
		}

		var allPatterns []string
		byAsset := make(map[int64]struct {
			name     string
			patterns []string
		})

		for _, item := range source {
			cmd := strings.TrimSpace(item.Command)
			if cmd == "" {
				continue
			}
			permission.SaveGrantPatternsForApproval(ctx, sessionID, item.AssetID, item.AssetName, item.Type, cmd, origin)
			allPatterns = append(allPatterns, cmd)
			entry := byAsset[item.AssetID]
			entry.name = item.AssetName
			entry.patterns = append(entry.patterns, cmd)
			byAsset[item.AssetID] = entry
		}
		for assetID, entry := range byAsset {
			audit.WriteGrantSubmitAudit(ctx, assetID, entry.name, entry.patterns)
		}
		return allPatterns, nil
	}
}

func newDesktopMCPChecker(sessionID string) *permission.CommandPolicyChecker {
	confirmFn := func(ctx context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
		dataDir := approvalDataDir()
		sockPath := approval.SocketPath(dataDir)
		if err := dialApprovalSocket(sockPath); err != nil {
			logger.Ctx(ctx).Warn("mcp desktop approval unreachable", zap.Error(err))
			return permission.ApprovalResponse{Decision: "deny"}
		}
		token, err := bootstrap.ReadAuthToken(dataDir)
		if err != nil {
			logger.Ctx(ctx).Warn("mcp read auth token", zap.Error(err))
		}

		if kind == permission.ApprovalKindBatch {
			batchItems := make([]approval.BatchItem, 0, len(items))
			for _, it := range items {
				batchItems = append(batchItems, approval.BatchItem{
					Type:            it.Type,
					AssetID:         it.AssetID,
					AssetName:       it.AssetName,
					Command:         it.Command,
					Detail:          it.Detail,
					Action:          it.Action,
					Resource:        it.Resource,
					Resources:       it.Resources,
					RememberPattern: it.RememberPattern,
					Review:          it.Review,
				})
			}
			resp, err := approval.RequestApprovalWithToken(sockPath, token, approval.ApprovalRequest{
				Type:       "batch",
				SessionID:  sessionID,
				BatchItems: batchItems,
			})
			if err != nil || !resp.Approved {
				return permission.ApprovalResponse{Decision: "deny"}
			}
			return permission.ApprovalResponse{Decision: "allow"}
		}

		if len(items) == 0 {
			return permission.ApprovalResponse{Decision: "deny"}
		}
		it := items[0]
		resp, err := approval.RequestApprovalWithToken(sockPath, token, approval.ApprovalRequest{
			Type:            it.Type,
			AssetID:         it.AssetID,
			AssetName:       it.AssetName,
			Command:         it.Command,
			Detail:          it.Detail,
			SessionID:       sessionID,
			Action:          it.Action,
			Resource:        it.Resource,
			Resources:       it.Resources,
			RememberPattern: it.RememberPattern,
			Review:          it.Review,
		})
		if err != nil || !resp.Approved {
			return permission.ApprovalResponse{Decision: "deny"}
		}
		return permission.ApprovalResponse{Decision: "allow"}
	}

	checker := permission.NewCommandPolicyChecker(confirmFn)
	checker.SetGrantRequestFunc(func(ctx context.Context, items []permission.ApprovalItem, reason string) (bool, []string) {
		dataDir := approvalDataDir()
		sockPath := approval.SocketPath(dataDir)
		if err := dialApprovalSocket(sockPath); err != nil {
			logger.Ctx(ctx).Warn("mcp desktop grant approval unreachable", zap.Error(err))
			return false, nil
		}
		token, err := bootstrap.ReadAuthToken(dataDir)
		if err != nil {
			logger.Ctx(ctx).Warn("mcp read auth token", zap.Error(err))
		}
		grantItems := make([]approval.GrantItem, 0, len(items))
		for _, it := range items {
			grantItems = append(grantItems, approval.GrantItem{
				Type:      it.Type,
				AssetID:   it.AssetID,
				AssetName: it.AssetName,
				GroupID:   it.GroupID,
				GroupName: it.GroupName,
				Command:   it.Command,
				Detail:    it.Detail,
			})
		}
		resp, err := approval.RequestApprovalWithToken(sockPath, token, approval.ApprovalRequest{
			Type:        "grant",
			SessionID:   sessionID,
			GrantItems:  grantItems,
			Description: reason,
		})
		if err != nil || !resp.Approved {
			return false, nil
		}
		finalItems := resp.EditedItems
		if len(finalItems) == 0 {
			finalItems = grantItems
		}
		patterns := make([]string, 0, len(finalItems))
		for _, it := range finalItems {
			patterns = append(patterns, it.Command)
		}
		return true, patterns
	})
	return checker
}

func newMCPListAuditTool() mcp.ExtraTool {
	return mcp.ExtraTool{
		Spec: mcp.ToolSpec{
			Name: "list_audit",
			Description: "Read-only query of OpsKat audit log rows (newest first): time, source, asset, tool, command summary, " +
				"result, and decision source.",
			InputSchema: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"asset":    {Type: "string", Description: "Optional asset filter (name, group/name, or numeric ID)."},
					"source":   {Type: "string", Description: "Optional source filter: " + strings.Join(audit_svc.Sources, ", ")},
					"tool":     {Type: "string", Description: "Optional tool name filter (e.g. exec, cp, batch_exec)."},
					"result":   {Type: "string", Description: "Optional result filter: " + strings.Join(audit_svc.Results, ", ")},
					"decision": {Type: "string", Description: "Optional decision filter: " + strings.Join(audit_svc.Decisions, ", ")},
					"decider":  {Type: "string", Description: "Optional decider filter: " + strings.Join(aictx.DeciderGroups, ", ")},
					"grep":     {Type: "string", Description: "Optional case-insensitive substring filter on command or asset name."},
					"since":    {Type: "string", Description: "Optional duration window, e.g. 30m, 6h, 7d."},
					"limit":    {Type: "number", Description: fmt.Sprintf("Maximum rows to return (default %d).", defaultAuditListLimit)},
				},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
			q := audit_svc.Query{
				Source:   aictx.ArgString(args, "source"),
				ToolName: aictx.ArgString(args, "tool"),
				Result:   aictx.ArgString(args, "result"),
				Decision: aictx.ArgString(args, "decision"),
				Decider:  aictx.ArgString(args, "decider"),
				Keyword:  aictx.ArgString(args, "grep"),
				Limit:    aictx.ArgInt(args, "limit"),
			}
			if q.Limit <= 0 {
				q.Limit = defaultAuditListLimit
			}
			if since := aictx.ArgString(args, "since"); since != "" {
				window, err := parseSince(since)
				if err != nil {
					return nil, fmt.Errorf("invalid since %q: use a positive duration such as 30m, 6h or 7d", since)
				}
				q.StartTime = time.Now().Add(-window).Unix()
			}
			if assetRef := aictx.ArgString(args, "asset"); assetRef != "" {
				id, err := resolveAssetID(ctx, assetRef)
				if err != nil {
					return nil, err
				}
				q.AssetIDs = []int64{id}
			}

			logs, _, err := audit_svc.List(ctx, q)
			if err != nil {
				return nil, err
			}

			type auditRow struct {
				Time           string `json:"time"`
				Source         string `json:"source"`
				Asset          string `json:"asset"`
				Tool           string `json:"tool"`
				Command        string `json:"command"`
				Decision       string `json:"decision,omitempty"`
				DecisionSource string `json:"decision_source,omitempty"`
				Success        bool   `json:"success"`
				Error          string `json:"error,omitempty"`
			}
			rows := make([]auditRow, 0, len(logs))
			for _, row := range logs {
				rows = append(rows, auditRow{
					Time:           time.Unix(row.Createtime, 0).Format(time.RFC3339),
					Source:         row.Source,
					Asset:          auditAssetCell(row),
					Tool:           row.ToolName,
					Command:        row.Command,
					Decision:       row.Decision,
					DecisionSource: row.DecisionSource,
					Success:        row.Success == 1,
					Error:          row.Error,
				})
			}
			encoded, err := json.MarshalIndent(rows, "", "  ")
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{
				Content:           []mcp.ContentItem{{Type: "text", Text: string(encoded)}},
				StructuredContent: map[string]any{"rows": rows},
			}, nil
		},
	}
}
