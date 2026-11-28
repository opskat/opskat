package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
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
	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/mcp"
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

func cmdMCP(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	toolsetRaw := fs.String("toolset", string(mcp.ToolsetCore), "Exposed toolset: core or full")
	sessionFlag := fs.String("session", "", "Explicit grant session ID (default: isolated per-process UUID; never inherits CLI session)")
	var allowLocalDirs stringSliceFlag
	fs.Var(&allowLocalDirs, "allow-local-dir", "Restrict local files read or written by cp and object storage --file to this directory (repeatable)")
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

	srv := mcp.NewServer(mcp.ServerConfig{
		ServerVersion: configs.Version,
		Tools:         mcpTools(toolset, session),
		AuditWriter:   opsctlAuditWriter,
		SessionID:     session,
		Instructions:  mcpInstructions(ctx),
		PrepareCtx:    mcpCallContext(mcpApprover{sessionID: session}, normalizedLocalDirs),
	})

	// Clients stop a stdio server with SIGTERM as often as with SIGINT; either way the
	// in-flight calls are canceled and their audit rows written before the process exits.
	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(serveCtx, mcpStdio()); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// mcpCallContext equips every tool call with what this entry decides: approvals go to
// approver, local file access and asset/group writes are confirmed, SSH MFA is answered
// in the desktop app (stdin carries the protocol).
func mcpCallContext(approver mcpApprover, allowedLocalDirs []string) func(context.Context) context.Context {
	checker := approver.checker()
	// One gate for both directions of local file access, as the desktop AI uses one for
	// its local tools: "allow for this session" on a path is one record.
	localGate := approver.localToolGate()
	docGate := tool.NewDocGate()
	return func(ctx context.Context) context.Context {
		ctx = tool.WithDocGate(ctx, docGate)
		ctx = permission.WithPolicyChecker(ctx, checker)
		ctx = tool.WithDataWriteConfirmation(ctx)
		ctx = helper.WithLocalWriteGate(ctx, localGate)
		// Unlike the desktop AI, an external agent's uploads of local files go through
		// a gate too (helper.LocalReadGate explains why); --allow-local-dir replaces it.
		ctx = helper.WithLocalReadGate(ctx, localGate)
		if len(allowedLocalDirs) > 0 {
			ctx = helper.WithAllowedLocalDirs(ctx, allowedLocalDirs)
		}
		return withDesktopOnlyMFA(withDesktopOnlyConnectionPathMFA(ctx))
	}
}

// mcpInstructions is the guidance sent at initialize: the built-in AI's tool guidance
// plus what only this entry does.
func mcpInstructions(ctx context.Context) string {
	return runner.ToolGuidance(runner.AssetTypeSkills(), runner.DefinedCustomTypes(ctx)) +
		"\n\nEvery put_asset and put_group call asks the user to confirm it and cannot be pre-approved, so put all changes to an asset or group in one call."
}

// mcpTools is what opsctl mcp serves for toolset: the built-in AI's tools, the ones
// naming assets wrapped to activate those assets' extensions first, plus list_audit
// in the full toolset.
func mcpTools(toolset mcp.Toolset, session string) []agent.Tool {
	served := mcp.FilterTools(tool.Tools(), toolset)
	tools := make([]agent.Tool, 0, len(served)+1)
	for _, t := range served {
		if refs, ok := mcpToolAssetRefs[t.Name()]; ok {
			t = extensionActivatingTool{Tool: t, refs: refs}
		}
		tools = append(tools, t)
	}
	if toolset == mcp.ToolsetFull {
		tools = append(tools, newMCPListAuditTool(session))
	}
	return tools
}

func printMCPUsage() {
	fmt.Fprintf(os.Stderr, `Usage:
  opsctl mcp [flags]

Run a stdio MCP (Model Context Protocol) server exposing OpsKat tools.

Operations that need a human decision are asked in the MCP client's own prompt when
the client supports it (form elicitation: Claude Code, Codex, pi, ...), otherwise in
the running OpsKat desktop app. With neither available they are denied. SSH MFA is
always answered in the desktop app.

Flags:
  --toolset <core|full>
        Which tools to expose via tools/list (default: core).
        core - %s.
        full - All core tools plus %s.
  --session <id>
        Explicit grant session ID (default: isolated per-process UUID).
  --allow-local-dir <dir>
        Restrict the local files cp and object storage --file read or write to
        this directory (repeatable). Uploads from it are then not asked about.
`, strings.Join(mcpToolNames(mcp.ToolsetCore, nil), ", "),
		strings.Join(mcpToolNames(mcp.ToolsetFull, mcpToolNames(mcp.ToolsetCore, nil)), ", "))
}

// mcpToolNames lists the tools toolset serves, leaving out those in except.
func mcpToolNames(toolset mcp.Toolset, except []string) []string {
	var names []string
	for _, t := range mcpTools(toolset, "") {
		if !slices.Contains(except, t.Name()) {
			names = append(names, t.Name())
		}
	}
	return names
}

// mcpToolAssetRefs names, per tool, the assets a call operates on. opsctl loads an
// extension only when a command needs it (registerExtensionAssetTypes keeps just the
// descriptions), and this server lives as long as its client, so each call activates
// the extensions its assets need before running.
var mcpToolAssetRefs = map[string]func(ctx context.Context, input map[string]any) []string{
	"exec":       assetArgRef,
	"help":       assetArgRef,
	"put_asset":  assetArgRef,
	"batch_exec": func(_ context.Context, input map[string]any) []string { return tool.BatchCommandAssets(input) },
	"cp": func(ctx context.Context, input map[string]any) []string {
		var refs []string
		for _, key := range []string{"src", "dst"} {
			asset, _, err := helper.ParseTransferEndpoint(ctx, aictx.ArgString(input, key), assetref.Resolve)
			if err == nil && asset != nil {
				refs = append(refs, strconv.FormatInt(asset.ID, 10))
			}
		}
		return refs
	},
}

func assetArgRef(_ context.Context, input map[string]any) []string {
	return []string{aictx.ArgString(input, "asset")}
}

// extensionActivatingTool runs its tool after activating, in this process, the
// extensions owning the assets the call names.
type extensionActivatingTool struct {
	agent.Tool
	refs func(ctx context.Context, input map[string]any) []string
}

func (e extensionActivatingTool) Serial() bool { return mcp.IsSerial(e.Tool) }

func (e extensionActivatingTool) Call(ctx context.Context, input map[string]any) (*agent.ToolResultBlock, error) {
	for _, ref := range e.refs(ctx, input) {
		if err := activateExtensionsForRef(ctx, ref); err != nil {
			return nil, err
		}
	}
	return e.Tool.Call(ctx, input)
}

// activateExtensionsForRef activates the extension providing the referenced asset's
// type and its database driver. An extension that fails to load, or that the user
// disabled since this server started, is refused by name (extension_svc.Activate checks
// on every call) and works again once fixed or re-enabled. A reference that does not
// resolve activates nothing: the tool reports the canonical resolution error itself.
func activateExtensionsForRef(ctx context.Context, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return nil
	}
	asset, err := resolveAsset(ctx, ref)
	if err != nil || asset == nil {
		return nil //nolint:nilerr // the tool surfaces the canonical asset resolution error
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
