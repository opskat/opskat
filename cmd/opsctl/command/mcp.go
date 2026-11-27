package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/cago-frame/agents/agent"
	cagotool "github.com/cago-frame/agents/tool"
	"github.com/cago-frame/cago/configs"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/mcp"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_put_svc"
	"github.com/opskat/opskat/internal/service/audit_svc"
)

const mcpMaxAuditListLimit = 200

// mcpRealStdout is the process stdout captured in Execute() before os.Stdout is
// redirected to os.Stderr, so only the MCP transport ever writes JSON-RPC frames to it.
var mcpRealStdout *os.File

// mcpStdio returns the MCP transport; overridden in tests.
var mcpStdio = func() mcpsdk.Transport {
	return &mcpsdk.IOTransport{Reader: os.Stdin, Writer: mcpRealStdout}
}

type serialTool interface {
	Serial() bool
}

func isToolSerial(t agent.Tool) bool {
	if st, ok := t.(serialTool); ok {
		return st.Serial()
	}
	return false
}

func cmdMCP(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	toolsetRaw := fs.String("toolset", string(mcp.ToolsetCore), "Exposed toolset: core or full")
	sessionFlag := fs.String("session", "", "Explicit grant session ID (default: isolated per-process UUID; never inherits CLI session)")
	var allowLocalDirs stringSliceFlag
	fs.Var(&allowLocalDirs, "allow-local-dir", "Restrict cp local filesystem endpoints to this directory (repeatable)")
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

	toolset, err := mcp.ParseToolset(*toolsetRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	normalizedLocalDirs, err := helper.NormalizeAllowedLocalDirs(allowLocalDirs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Isolate MCP sessions by default so external agents never inherit CLI 24h grants
	// or pollute other CLI/MCP sessions unless the caller explicitly opts in via --session.
	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		session = "mcp_" + uuid.New().String()
	}

	approver := mcpApprover{sessionID: session}
	checker := approver.checker()
	localWriteGate := approver.localToolGate()
	docGate := tool.NewDocGate()
	tools := wrapMCPTools(mcp.FilterTools(tool.Tools(), toolset), normalizedLocalDirs)
	if toolset == mcp.ToolsetFull {
		tools = append(tools, newMCPListAuditTool(session))
	}

	srv := mcp.NewServer(mcp.ServerConfig{
		ServerVersion: configs.Version,
		Tools:         tools,
		AuditWriter:   opsctlAuditWriter,
		SessionID:     session,
		PrepareCtx: func(callCtx context.Context) context.Context {
			callCtx = tool.WithDocGate(callCtx, docGate)
			callCtx = permission.WithPolicyChecker(callCtx, checker)
			callCtx = helper.WithLocalWriteGate(callCtx, localWriteGate)
			if len(normalizedLocalDirs) > 0 {
				callCtx = helper.WithAllowedLocalDirs(callCtx, normalizedLocalDirs)
			} else {
				callCtx = helper.WithLocalReadGate(callCtx, newMCPLocalReadGate())
			}
			return withDesktopOnlyMFA(withDesktopOnlyConnectionPathMFA(callCtx))
		},
		OnMutate: func(toolName string) {
			if refreshesDesktopUI(toolName) {
				notifyDesktopAssetChanged()
			}
		},
	})

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	if err := srv.Run(serveCtx, mcpStdio()); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func printMCPUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl mcp [flags]

Run a stdio MCP (Model Context Protocol) server exposing OpsKat tools.

Operations that need a human decision are asked in the MCP client's own prompt when
the client supports it (form elicitation: Claude Code, Codex, pi, ...), otherwise in
the running OpsKat desktop app. With neither available they are denied. SSH MFA is
always answered in the desktop app.

Flags:
  --toolset <core|full>
        Which tools to expose via tools/list (default: core).
        core - list_assets, get_asset, help, exec, batch_exec, cp, request_permission.
        full - All core tools plus list_groups, get_group, list_credentials,
               get_credential, put_asset, put_group, delete_asset,
               delete_group, get_asset_secret, list_audit (session-scoped).
  --session <id>
        Explicit grant session ID (default: isolated per-process UUID).
  --allow-local-dir <dir>
        Restrict cp local filesystem endpoints to this directory (repeatable).
`)
}

// wrapMCPTools wraps MCP-exposed tools with:
//   - on-demand extension activation & live disable checks (exec, batch_exec, help, put_asset, cp)
//   - mandatory human confirmation gates on metadata writes (put_asset, put_group)
//   - local filesystem read/write guardrails (cp)
func wrapMCPTools(tools []agent.Tool, allowedLocalDirs []string) []agent.Tool {
	out := make([]agent.Tool, 0, len(tools))
	for _, t := range tools {
		switch t.Name() {
		case "exec":
			out = append(out, &extensionActivatingTool{inner: t})
		case "batch_exec":
			out = append(out, &batchExtensionActivatingTool{inner: t})
		case "help":
			out = append(out, &helpExtensionActivatingTool{inner: t})
		case "put_asset":
			out = append(out, &guardedPutAssetTool{inner: t})
		case "put_group":
			out = append(out, &guardedPutGroupTool{inner: t})
		case "cp":
			out = append(out, &guardedCpTool{inner: t, allowedLocalDirs: allowedLocalDirs})
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
	if err != nil {
		return nil //nolint:nilerr // let underlying tool surface the canonical asset resolution error
	}
	if asset == nil {
		return nil
	}
	if err := refusedExtensionError(ctx, asset); err != nil {
		revokeDisabledExtension(asset.Type)
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

func revokeDisabledExtension(assetType string) {
	extName, isExt, err := extensionOwnerOf(assetType)
	if err != nil || !isExt || extName == "" {
		return
	}
	extensionContributions.Revoke(extName)
}

type extensionActivatingTool struct {
	inner agent.Tool
}

func (e *extensionActivatingTool) Name() string         { return e.inner.Name() }
func (e *extensionActivatingTool) Description() string  { return e.inner.Description() }
func (e *extensionActivatingTool) Schema() agent.Schema { return e.inner.Schema() }
func (e *extensionActivatingTool) Serial() bool         { return isToolSerial(e.inner) }

func (e *extensionActivatingTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	if err := activateExtensionsForRef(ctx, aictx.ArgString(input, "asset")); err != nil {
		return nil, err
	}
	return e.inner.Call(ctx, input)
}

type helpExtensionActivatingTool struct {
	inner agent.Tool
}

func (h *helpExtensionActivatingTool) Name() string         { return h.inner.Name() }
func (h *helpExtensionActivatingTool) Description() string  { return h.inner.Description() }
func (h *helpExtensionActivatingTool) Schema() agent.Schema { return h.inner.Schema() }
func (h *helpExtensionActivatingTool) Serial() bool         { return isToolSerial(h.inner) }

func (h *helpExtensionActivatingTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	if err := activateExtensionsForRef(ctx, aictx.ArgString(input, "asset")); err != nil {
		return nil, err
	}
	return h.inner.Call(ctx, input)
}

type batchExtensionActivatingTool struct {
	inner agent.Tool
}

func (b *batchExtensionActivatingTool) Name() string         { return b.inner.Name() }
func (b *batchExtensionActivatingTool) Description() string  { return b.inner.Description() }
func (b *batchExtensionActivatingTool) Schema() agent.Schema { return b.inner.Schema() }
func (b *batchExtensionActivatingTool) Serial() bool         { return isToolSerial(b.inner) }

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

// guardedPutAssetTool requires one-shot human confirmation (never grantable or permanent)
// before creating or updating an asset, matching opsctl create/update's security contract.
// Only non-secret metadata and config key names are displayed in the approval prompt.
type guardedPutAssetTool struct {
	inner agent.Tool
}

func (g *guardedPutAssetTool) Name() string         { return g.inner.Name() }
func (g *guardedPutAssetTool) Description() string  { return g.inner.Description() }
func (g *guardedPutAssetTool) Schema() agent.Schema { return g.inner.Schema() }
func (g *guardedPutAssetTool) Serial() bool         { return isToolSerial(g.inner) }

func (g *guardedPutAssetTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	// Always install the fail-closed top-level audit projection immediately so any
	// early error or user denial never falls back to marshaling raw input["config"]
	// (which may carry plaintext passwords, private keys, or secret tokens) into audit_logs.
	aictx.RecordAuditRequest(ctx, tool.PutAssetTopLevelAuditArgs(input))

	ref := aictx.ArgString(input, "asset")
	if ref != "" {
		if err := activateExtensionsForRef(ctx, ref); err != nil {
			return nil, err
		}
	}

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return nil, err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return nil, errors.New("put_asset requires an interactive approval channel, none is wired")
	}

	op, assetID, assetName, prepared, err := preparePutAssetForMCPApproval(ctx, input)
	if err != nil {
		return nil, err
	}
	aictx.RecordAuditRequest(ctx, prepared.SafeAuditArgs())

	cmdSummary := fmt.Sprintf("%s asset %q", op, assetName)
	if typ := prepared.SafeApprovalDetail()["type"]; typ != nil && typ != "" {
		cmdSummary += fmt.Sprintf(" (type=%v)", typ)
	}
	detail := buildSafePutAssetApprovalDetail(op, ref, input, prepared)
	items := []permission.ApprovalItem{{
		Type:      op,
		AssetID:   assetID,
		AssetName: assetName,
		Command:   cmdSummary,
		Detail:    detail,
	}}
	resp := confirm(ctx, permission.ApprovalKindOnce, items)
	if !isOneShotApproved(resp, items) {
		aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny})
		return &agent.ToolResultBlock{
			Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("USER DENIED: The user has denied %s. Stop the current task immediately.", cmdSummary)}},
			IsError: true,
		}, nil
	}
	aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow})
	return g.inner.Call(ctx, input)
}

func isOneShotApproved(resp permission.ApprovalResponse, items []permission.ApprovalItem) bool {
	parsed, err := permission.ParseApprovalResponse(permission.ApprovalKindOnce, resp, items)
	return err == nil && parsed.Decision == permission.ApprovalAllow
}

func preparePutAssetForMCPApproval(ctx context.Context, input map[string]any) (op string, assetID int64, assetName string, prepared *asset_put_svc.Prepared, err error) {
	config := map[string]any{}
	if raw, exists := input["config"]; exists {
		m, ok := raw.(map[string]any)
		if !ok {
			return "", 0, "", nil, fmt.Errorf("parameter config must be an object, got %T", raw)
		}
		config = m
	}

	ref := aictx.ArgString(input, "asset")
	var asset *asset_entity.Asset
	if ref == "" {
		op = "create"
		name := aictx.ArgString(input, "name")
		if name == "" {
			return "", 0, "", nil, errors.New("missing required parameter: name (creating an asset needs a name; pass asset=<id-or-name> instead to update an existing one)")
		}
		assetType := aictx.ArgString(input, "type")
		if assetType == "" {
			assetType = asset_entity.AssetTypeSSH
		}
		asset, err = assettype.NewAsset(ctx, assetType)
		if errors.Is(err, assettype.ErrUnknownType) {
			return "", 0, "", nil, fmt.Errorf("unsupported asset type %q; supported: %s, or a custom type slug",
				assetType, strings.Join(permission.RegisteredHelpTypes(), ", "))
		}
		if err != nil {
			return "", 0, "", nil, err
		}
		asset.Name = name
		asset.Icon = aictx.ArgString(input, "icon")
		asset.GroupID = aictx.ArgInt64(input, "group_id")
		asset.Description = aictx.ArgString(input, "description")
	} else {
		op = "update"
		asset, err = assetref.Resolve(ctx, ref)
		if err != nil {
			return "", 0, "", nil, err
		}
		if declared := aictx.ArgString(input, "type"); declared != "" {
			if err := permission.AssertAssetType(asset, declared); err != nil {
				return "", 0, "", nil, err
			}
		}
		cloned := *asset
		if name := aictx.ArgString(input, "name"); name != "" {
			cloned.Name = name
		}
		if _, ok := input["description"]; ok {
			cloned.Description = aictx.ArgString(input, "description")
		}
		if gid := aictx.ArgInt64(input, "group_id"); gid > 0 {
			cloned.GroupID = gid
		}
		if icon := aictx.ArgString(input, "icon"); icon != "" {
			cloned.Icon = icon
		}
		asset = &cloned
	}

	prepared, err = asset_put_svc.Prepare(ctx, asset_put_svc.Request{
		Asset:  asset,
		Config: config,
	})
	if err != nil {
		return "", 0, "", nil, fmt.Errorf("failed to %s asset: %w", op, err)
	}
	return op, asset.ID, asset.Name, prepared, nil
}

func buildSafePutAssetApprovalDetail(op, ref string, input map[string]any, prepared *asset_put_svc.Prepared) string {
	safe := prepared.SafeApprovalDetail()
	safe["operation"] = op
	if ref != "" {
		safe["asset"] = ref
	}
	if gid := aictx.ArgInt64(input, "group_id"); gid > 0 {
		safe["group_id"] = gid
	}
	if desc := aictx.ArgString(input, "description"); desc != "" {
		safe["description"] = desc
	}
	b, err := json.Marshal(safe)
	if err != nil {
		return op + " asset"
	}
	return string(b)
}

// guardedPutGroupTool requires one-shot human confirmation before creating or updating a group.
type guardedPutGroupTool struct {
	inner agent.Tool
}

func (g *guardedPutGroupTool) Name() string         { return g.inner.Name() }
func (g *guardedPutGroupTool) Description() string  { return g.inner.Description() }
func (g *guardedPutGroupTool) Schema() agent.Schema { return g.inner.Schema() }
func (g *guardedPutGroupTool) Serial() bool         { return isToolSerial(g.inner) }

func (g *guardedPutGroupTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return nil, err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return nil, errors.New("put_group requires an interactive approval channel, none is wired")
	}

	id := aictx.ArgInt64(input, "id")
	name := aictx.ArgString(input, "name")
	op := "create"
	if id > 0 {
		op = "update"
	}
	cmdSummary := fmt.Sprintf("%s group %q", op, name)
	if id > 0 {
		cmdSummary = fmt.Sprintf("update group #%d", id)
		if name != "" {
			cmdSummary += fmt.Sprintf(" (%q)", name)
		}
	}
	detailMap := map[string]any{"operation": op}
	if id > 0 {
		detailMap["id"] = id
	}
	if name != "" {
		detailMap["name"] = name
	}
	if pid := aictx.ArgInt64(input, "parent_id"); pid > 0 {
		detailMap["parent_id"] = pid
	}
	detailBytes, _ := json.Marshal(detailMap)

	items := []permission.ApprovalItem{{
		Type:      op,
		GroupID:   id,
		GroupName: name,
		Command:   cmdSummary,
		Detail:    string(detailBytes),
	}}
	resp := confirm(ctx, permission.ApprovalKindOnce, items)
	if !isOneShotApproved(resp, items) {
		aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny})
		return &agent.ToolResultBlock{
			Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("USER DENIED: The user has denied %s. Stop the current task immediately.", cmdSummary)}},
			IsError: true,
		}, nil
	}
	aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow})
	return g.inner.Call(ctx, input)
}

// guardedCpTool enforces local filesystem guardrails on cp:
//   - when --allow-local-dir is set, every local endpoint (read or write) must reside within
//     an allowed directory (after symlink resolution);
//   - when --allow-local-dir is NOT set and src is a local path (upload), an explicit one-shot
//     human confirmation is required so a permissive remote cp:write policy cannot silently
//     exfiltrate host files like ~/.ssh/id_rsa or opskat.db.
type guardedCpTool struct {
	inner            agent.Tool
	allowedLocalDirs []string
}

func (g *guardedCpTool) Name() string         { return g.inner.Name() }
func (g *guardedCpTool) Description() string  { return g.inner.Description() }
func (g *guardedCpTool) Schema() agent.Schema { return g.inner.Schema() }
func (g *guardedCpTool) Serial() bool         { return isToolSerial(g.inner) }

func (g *guardedCpTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	srcRaw := aictx.ArgString(input, "src")
	dstRaw := aictx.ArgString(input, "dst")
	if srcRaw == "" || dstRaw == "" {
		return g.inner.Call(ctx, input)
	}

	srcAsset, srcPath, srcErr := helper.ParseTransferEndpoint(ctx, srcRaw, assetref.Resolve)
	dstAsset, dstPath, dstErr := helper.ParseTransferEndpoint(ctx, dstRaw, assetref.Resolve)

	if srcErr == nil && srcAsset != nil {
		if err := activateExtensionsForRef(ctx, fmt.Sprintf("%d", srcAsset.ID)); err != nil {
			return nil, err
		}
	}
	if dstErr == nil && dstAsset != nil {
		if err := activateExtensionsForRef(ctx, fmt.Sprintf("%d", dstAsset.ID)); err != nil {
			return nil, err
		}
	}

	// Enforce --allow-local-dir whitelist on every local endpoint (both read and write).
	if len(g.allowedLocalDirs) > 0 {
		if srcErr == nil && srcAsset == nil {
			if err := helper.VerifyLocalPathAllowed(srcPath, g.allowedLocalDirs); err != nil {
				return nil, fmt.Errorf("local source path blocked by --allow-local-dir: %w", err)
			}
		}
		if dstErr == nil && dstAsset == nil {
			if err := helper.VerifyLocalPathAllowed(dstPath, g.allowedLocalDirs); err != nil {
				return nil, fmt.Errorf("local destination path blocked by --allow-local-dir: %w", err)
			}
		}
	} else if srcErr == nil && srcAsset == nil && dstErr == nil && dstAsset != nil {
		// No directory whitelist configured: require explicit confirmation before reading
		// any local host file for upload to a remote asset.
		if !filepath.IsAbs(srcPath) {
			return g.inner.Call(ctx, input)
		}
		if err := confirmMCPLocalRead(ctx, dstAsset.ID, dstAsset.Name, srcPath, fmt.Sprintf("cp %s → %s", srcRaw, dstRaw)); err != nil {
			if errors.Is(err, errMCPLocalReadDenied) {
				aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny})
				return &agent.ToolResultBlock{
					Content: []agent.ContentBlock{agent.TextBlock{Text: mcpLocalReadDeniedMsg}},
					IsError: true,
				}, nil
			}
			return nil, err
		}
	}

	return g.inner.Call(ctx, input)
}

const mcpLocalReadDeniedMsg = "USER DENIED: The user has denied reading the local file for upload. Stop the current task immediately."

var errMCPLocalReadDenied = errors.New("local file read denied by user")

func confirmMCPLocalRead(ctx context.Context, assetID int64, assetName, localPath, detail string) error {
	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return err
	}
	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return errors.New("uploading a local file requires an interactive approval channel, none is wired")
	}
	items := []permission.ApprovalItem{{
		Type:      "local_read",
		AssetID:   assetID,
		AssetName: assetName,
		Command:   fmt.Sprintf("read local %s (%s)", filepath.Clean(localPath), detail),
		Detail:    detail,
	}}
	resp := confirm(ctx, permission.ApprovalKindOnce, items)
	if !isOneShotApproved(resp, items) {
		return errMCPLocalReadDenied
	}
	return nil
}

type mcpLocalReadGate struct{}

func newMCPLocalReadGate() helper.LocalReadGate { return mcpLocalReadGate{} }

func (mcpLocalReadGate) CheckLocalRead(ctx context.Context, assetID int64, assetName, localPath, detail string) error {
	err := confirmMCPLocalRead(ctx, assetID, assetName, localPath, detail)
	if errors.Is(err, errMCPLocalReadDenied) {
		return fmt.Errorf("%s", mcpLocalReadDeniedMsg)
	}
	return err
}

// mcpListAuditTool is list_audit: an ordinary agent tool (so it runs through the same
// dispatcher and audit middleware as every other tool) that also declares its side
// effects, since it lives outside the built-in tool registry.
type mcpListAuditTool struct {
	*cagotool.RawTool
}

func (mcpListAuditTool) Behavior() tool.Behavior {
	return tool.Behavior{ReadOnly: true, Idempotent: true}
}

func newMCPListAuditTool(sessionID string) agent.Tool {
	return mcpListAuditTool{&cagotool.RawTool{
		NameStr: "list_audit",
		DescStr: "Read-only query of audit log rows produced by this MCP session (newest first): " +
			"time, source, asset, tool, command summary, result, and decision source.",
		SchemaVal: agent.Schema{
			Type: "object",
			Properties: map[string]*agent.Property{
				"asset":    {Type: "string", Description: "Optional asset filter (name, group/name, or numeric ID)."},
				"tool":     {Type: "string", Description: "Optional tool name filter (e.g. exec, cp, batch_exec)."},
				"result":   {Type: "string", Description: "Optional result filter: " + strings.Join(audit_svc.Results, ", ")},
				"decision": {Type: "string", Description: "Optional decision filter: " + strings.Join(audit_svc.Decisions, ", ")},
				"decider":  {Type: "string", Description: "Optional decider filter: " + strings.Join(aictx.DeciderGroups, ", ")},
				"grep":     {Type: "string", Description: "Optional case-insensitive substring filter on command or asset name."},
				"since":    {Type: "string", Description: "Optional duration window, e.g. 30m, 6h, 7d."},
				"limit":    {Type: "number", Description: fmt.Sprintf("Maximum rows to return (default %d, max %d).", defaultAuditListLimit, mcpMaxAuditListLimit)},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (*agent.ToolResultBlock, error) {
			limit := aictx.ArgInt(args, "limit")
			if limit <= 0 {
				limit = defaultAuditListLimit
			}
			if limit > mcpMaxAuditListLimit {
				return nil, fmt.Errorf("limit %d exceeds maximum allowed %d", limit, mcpMaxAuditListLimit)
			}
			q := audit_svc.Query{
				Source:    mcp.AuditSource,
				SessionID: sessionID,
				ToolName:  aictx.ArgString(args, "tool"),
				Result:    aictx.ArgString(args, "result"),
				Decision:  aictx.ArgString(args, "decision"),
				Decider:   aictx.ArgString(args, "decider"),
				Keyword:   aictx.ArgString(args, "grep"),
				Limit:     limit,
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
			return cagotool.TextResult(string(encoded)), nil
		},
	}}
}
