package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
	"go.uber.org/zap"

	"golang.org/x/crypto/ssh"
)

// batchInput is the JSON input format for the batch command.
type batchInput struct {
	Commands []batchCommand `json:"commands"`
}

type batchCommand struct {
	Asset   string `json:"asset"`
	Type    string `json:"type,omitempty"` // "exec"|"sql"|"redis"|"mongo", default "exec"
	Command string `json:"command"`
}

// batchResult is the per-command result in the JSON output.
type batchResult struct {
	AssetID   int64  `json:"asset_id"`
	AssetName string `json:"asset_name"`
	Type      string `json:"type"`
	Command   string `json:"command"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr,omitempty"`
	Error     string `json:"error,omitempty"`
}

type batchOutput struct {
	Results []batchResult `json:"results"`
}

// resolvedBatchCmd is a batch command with resolved asset info.
type resolvedBatchCmd struct {
	asset    *asset_entity.Asset
	cmdType  string // "exec"|"sql"|"redis"|"mongo"
	command  string
	decision *aictx.CheckResult // 策略预检结果，用于审计
}

var validBatchTypes = map[string]bool{"exec": true, "sql": true, "redis": true, "mongo": true}

// batchAssertPrefixType checks the batch item's prefix (cmdType) against the
// resolved asset's real type. The prefix grammar is unchanged (validBatchTypes /
// parseBatchArg above) but its meaning has shifted from "select a handler" to
// "assert a type" — same shift opsctl exec's --type flag went through
// (permission.AssertAssetType, exec.go).
//
// "exec" is treated as no assertion (declared=="", which AssertAssetType already
// treats as "skip"), not as "assert ssh": both parseBatchArg's bare 'asset:command'
// form and parseBatchInput's JSON-with-no-type both normalize to cmdType=="exec"
// before this is ever called (see the Step 2 loop in cmdBatch) — there is no way to
// tell "user wrote exec:" apart from "user wrote nothing" once parsing is done, and
// the whole point of this task is that a bare, unprefixed entry must run against any
// asset type, not just ssh. Only the prefixes that can *only* come from an explicit,
// unambiguous prefix — sql/redis/mongo — are real assertions.
func batchAssertPrefixType(asset *asset_entity.Asset, cmdType string) error {
	declared := cmdType
	if declared == "exec" {
		declared = ""
	}
	return permission.AssertAssetType(asset, declared)
}

func cmdBatch(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printBatchUsage()
		return 0
	}

	// Step 1: Parse input
	commands, err := parseBatchInput(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(commands) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no commands provided")
		printBatchUsage()
		return 1
	}

	// Step 2: Resolve all assets and assert the declared type. Either failure is now
	// terminal for that one item only (results[i] gets an error, ExitCode -1, and is
	// never touched again) — one bad entry no longer aborts the whole batch; the rest
	// still runs. resolved[i].asset == nil marks a terminal entry for Step 3 to skip.
	resolved := make([]resolvedBatchCmd, len(commands))
	results := make([]batchResult, len(commands))
	auditCtx := aictx.WithSessionID(ctx, session)
	auditCtx = aictx.WithAuditSource(auditCtx, "opsctl")

	for i, cmd := range commands {
		cmdType := cmd.Type // never "" here — parseBatchArg/parseBatchInput both default it to "exec"
		results[i] = batchResult{AssetName: cmd.Asset, Type: cmdType, Command: cmd.Command, ExitCode: -1}

		asset, resolveErr := resolveAsset(ctx, cmd.Asset)
		if resolveErr != nil {
			results[i].Error = fmt.Sprintf("resolve asset: %v", resolveErr)
			argsJSON := fmt.Sprintf(`{"asset":%q,"command":%q}`, cmd.Asset, truncateStr(cmd.Command, 200))
			writeOpsctlAudit(auditCtx, batchAuditTool, argsJSON, "", resolveErr, nil)
			continue
		}
		results[i].AssetID = asset.ID
		results[i].AssetName = asset.Name

		if assertErr := batchAssertPrefixType(asset, cmdType); assertErr != nil {
			results[i].Error = assertErr.Error()
			argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, asset.ID, truncateStr(cmd.Command, 200))
			writeOpsctlAudit(auditCtx, batchAuditTool, argsJSON, "", assertErr, nil)
			continue
		}

		resolved[i] = resolvedBatchCmd{
			asset:   asset,
			cmdType: cmdType,
			command: cmd.Command,
		}
	}

	// Step 3: Policy pre-check — split into auto-allow / auto-deny / need-confirm.
	// Entries that failed Step 2 (resolved[i].asset == nil) are already terminal —
	// results[i] carries their error — and never enter a bucket.
	type permBucket struct {
		idx    int
		result aictx.CheckResult
	}
	var autoAllow, autoDeny, needConfirm []permBucket

	for i, cmd := range resolved {
		if cmd.asset == nil {
			continue
		}
		permCtx := aictx.WithSessionID(ctx, session)
		pr := permission.CheckPermission(permCtx, cmd.asset.Type, cmd.asset.ID, cmd.command)
		prCopy := pr
		resolved[i].decision = &prCopy
		bucket := permBucket{idx: i, result: pr}
		switch pr.Decision {
		case aictx.Allow:
			autoAllow = append(autoAllow, bucket)
		case aictx.Deny:
			autoDeny = append(autoDeny, bucket)
		default:
			needConfirm = append(needConfirm, bucket)
		}
	}

	// Fill in denied results + write audit for each denied command
	for _, b := range autoDeny {
		cmd := resolved[b.idx]
		results[b.idx].Error = fmt.Sprintf("denied by policy: %s", b.result.Message)
		argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, cmd.asset.ID, truncateStr(cmd.command, 200))
		writeOpsctlAudit(auditCtx, batchAuditTool, argsJSON, "", fmt.Errorf("denied by policy: %s", b.result.Message), cmd.decision)
	}

	// Determine which commands to execute
	execSet := make(map[int]bool)
	for _, b := range autoAllow {
		execSet[b.idx] = true
	}

	// Step 4: Batch approval for need-confirm commands
	if len(needConfirm) > 0 {
		batchItems := make([]approval.BatchItem, 0, len(needConfirm))
		for _, b := range needConfirm {
			cmd := resolved[b.idx]
			batchItems = append(batchItems, approval.BatchItem{
				Type:      cmd.cmdType,
				AssetID:   cmd.asset.ID,
				AssetName: cmd.asset.Name,
				Command:   cmd.command,
			})
		}

		approvalResult, approvalErr := requireBatchApproval(batchItems, session)
		if approvalErr != nil {
			// All need-confirm commands are denied — write audit for each
			for _, b := range needConfirm {
				cmd := resolved[b.idx]
				results[b.idx].Error = fmt.Sprintf("approval failed: %v", approvalErr)
				argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, cmd.asset.ID, truncateStr(cmd.command, 200))
				decision := &aictx.CheckResult{Decision: aictx.Deny, DecisionSource: approvalResult.DecisionSource}
				writeOpsctlAudit(auditCtx, batchAuditTool, argsJSON, "", approvalErr, decision)
			}
		} else {
			session = approvalResult.SessionID
			auditCtx = aictx.WithSessionID(auditCtx, session)
			// Update decision to user_allow for approved commands
			for _, b := range needConfirm {
				resolved[b.idx].decision = &aictx.CheckResult{
					Decision:       aictx.Allow,
					DecisionSource: aictx.SourceUserAllow,
				}
				execSet[b.idx] = true
			}
		}
	}

	// Step 5: Parallel execution
	if len(execSet) > 0 {
		const maxConcurrency = 10
		sem := make(chan struct{}, maxConcurrency)
		var wg sync.WaitGroup

		for idx := range execSet {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				cmd := resolved[i]
				results[i] = executeBatchItem(auditCtx, handlers, cmd)
			}(idx)
		}
		wg.Wait()
	}

	// Step 6: Output JSON
	output := batchOutput{Results: results}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding output: %v\n", err)
		return 1
	}

	// Exit 0 if batch mechanism succeeded (even if individual commands failed)
	// Exit 1 only if ALL commands failed
	allFailed := true
	for _, r := range results {
		if r.Error == "" && r.ExitCode == 0 {
			allFailed = false
			break
		}
	}
	if allFailed && len(results) > 0 {
		return 1
	}
	return 0
}

// executeBatchItem runs a single command and returns the result.
func executeBatchItem(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, cmd resolvedBatchCmd) batchResult {
	result := batchResult{
		AssetID:   cmd.asset.ID,
		AssetName: cmd.asset.Name,
		Type:      cmd.cmdType,
		Command:   cmd.command,
	}

	switch cmd.cmdType {
	case "sql", "redis":
		result = executeBatchHandler(ctx, handlers, batchAuditTool, cmd, map[string]any{
			"asset":   strconv.FormatInt(cmd.asset.ID, 10),
			"command": cmd.command,
		})
	case "mongo":
		// Parse command as JSON: {"operation":"find","database":"db","collection":"col","query":"{}"}
		var mongoArgs struct {
			Operation  string `json:"operation"`
			Database   string `json:"database"`
			Collection string `json:"collection"`
			Query      string `json:"query"`
		}
		if err := json.Unmarshal([]byte(cmd.command), &mongoArgs); err != nil {
			result.Error = fmt.Sprintf("invalid mongo args JSON: %v", err)
			result.ExitCode = -1
			return result
		}
		// 结构化字段渲染成统一 exec 认识的富命令串，共用 helper.MongoCommand.Render。
		mongoCommand, renderErr := (&helper.MongoCommand{
			Op: mongoArgs.Operation, Database: mongoArgs.Database,
			Collection: mongoArgs.Collection, Query: mongoArgs.Query,
		}).Render()
		if renderErr != nil {
			result.Error = fmt.Sprintf("invalid mongo command: %v", renderErr)
			result.ExitCode = -1
			return result
		}
		result = executeBatchHandler(ctx, handlers, batchAuditTool, cmd, map[string]any{
			"asset":   strconv.FormatInt(cmd.asset.ID, 10),
			"command": mongoCommand,
		})
	default:
		// "exec" — the sentinel both parseBatchArg and parseBatchInput default an
		// unprefixed entry to (see cmdBatch's Step 2 / batchAssertPrefixType above),
		// not "unsupported type" anymore. Dispatch by the asset's real type: ssh
		// keeps the existing streaming channel (pipes, exit code), everything else
		// goes through the unified exec handler — exactly like cmdExec.go's non-ssh
		// branch. This is what makes a bare, unprefixed entry against a non-ssh asset
		// (database/redis/mongodb/etcd/kafka/k8s/...) work, not just ssh.
		if cmd.asset.IsSSH() {
			result = executeBatchExec(ctx, cmd)
		} else {
			result = executeBatchHandler(ctx, handlers, batchAuditTool, cmd, map[string]any{
				"asset":   strconv.FormatInt(cmd.asset.ID, 10),
				"command": cmd.command,
			})
		}
	}

	// Write audit log with decision from policy pre-check
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, cmd.asset.ID, truncateStr(cmd.command, 200))
	var execErr error
	if result.Error != "" {
		execErr = fmt.Errorf("%s", result.Error)
	}
	writeOpsctlAudit(ctx, batchAuditTool, argsJSON, result.Stdout, execErr, cmd.decision)

	return result
}

// executeBatchExec runs an SSH exec command and captures output.
func executeBatchExec(ctx context.Context, cmd resolvedBatchCmd) batchResult {
	result := batchResult{
		AssetID:   cmd.asset.ID,
		AssetName: cmd.asset.Name,
		Type:      cmd.cmdType,
		Command:   cmd.command,
	}

	outBuf := audit.NewLimitedBuffer(auditOutputLimit)
	errBuf := audit.NewLimitedBuffer(auditOutputLimit)

	if proxy := getSSHProxyClient(); proxy != nil {
		exitCode, execErr := proxy.Exec(sshpool.ProxyRequest{
			AssetID: cmd.asset.ID,
			Command: cmd.command,
		}, nil, outBuf, errBuf)
		result.ExitCode = exitCode
		result.Stdout = outBuf.String()
		result.Stderr = errBuf.String()
		if execErr != nil {
			result.Error = execErr.Error()
		}
		return result
	}

	// Fallback: direct SSH
	execErr := helper.ExecWithStdio(ctx, cmd.asset.ID, cmd.command, nil, outBuf, errBuf)
	result.Stdout = outBuf.String()
	result.Stderr = errBuf.String()
	if execErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(execErr, &exitErr) {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			result.ExitCode = -1
			result.Error = execErr.Error()
		}
	}
	return result
}

// executeBatchHandler runs a data command via the tool handler.
func executeBatchHandler(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, toolName string, cmd resolvedBatchCmd, params map[string]any) batchResult {
	result := batchResult{
		AssetID:   cmd.asset.ID,
		AssetName: cmd.asset.Name,
		Type:      cmd.cmdType,
		Command:   cmd.command,
	}

	ctx = aictx.WithAuditSource(ctx, "opsctl")
	// batch 不走 callHandler，得自己声明"已预检"：上面 Step 3 已经对每条命令跑过
	// permission.CheckPermission，need-confirm 的还聚合成一次桌面审批。handler 内部的
	// 权限检查是 fail-closed 的（permission.RequireCheckerOrPreapproved），而 opsctl 的
	// context 里没有 PolicyChecker——不声明的话这里会直接报 checker not available。
	ctx = permission.WithPreapproved(ctx)
	handler, ok := handlers[toolName]
	if !ok {
		result.Error = fmt.Sprintf("unknown tool: %s", toolName)
		result.ExitCode = -1
		return result
	}

	output, err := handler(ctx, params)
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		return result
	}

	result.Stdout = output
	result.ExitCode = 0
	return result
}

// requireBatchApproval sends a single batch approval request to the desktop app.
func requireBatchApproval(items []approval.BatchItem, session string) (ApprovalResult, error) {
	if session == "" {
		id := newSessionID()
		if err := writeActiveSession(id); err != nil {
			logger.Default().Warn("write active session", zap.Error(err))
		}
		session = id
	}

	dataDir := bootstrap.ResolvedDataDir()
	sockPath := approval.SocketPath(dataDir)

	authToken, err := bootstrap.ReadAuthToken(dataDir)
	if err != nil {
		logger.Default().Warn("read auth token", zap.Error(err))
	}

	// Build detail string for the request
	details := make([]string, 0, len(items))
	for _, item := range items {
		details = append(details, fmt.Sprintf("[%s] %s: %s", item.Type, item.AssetName, truncateStr(item.Command, 80)))
	}

	resp, err := approval.RequestApprovalWithToken(sockPath, authToken, approval.ApprovalRequest{
		Type:       "batch",
		Detail:     strings.Join(details, "\n"),
		SessionID:  session,
		BatchItems: items,
	})
	if err != nil {
		return ApprovalResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourcePolicyDeny,
			SessionID:      session,
		}, fmt.Errorf("desktop app is not running: %v", err)
	}
	if !resp.Approved {
		reason := resp.Reason
		if reason == "" {
			reason = "denied"
		}
		return ApprovalResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourceUserDeny,
			SessionID:      session,
		}, fmt.Errorf("batch denied: %s", reason)
	}

	return ApprovalResult{
		Decision:       aictx.Allow,
		DecisionSource: aictx.SourceUserAllow,
		SessionID:      session,
	}, nil
}

// parseBatchInput parses input from either stdin JSON or positional args.
func parseBatchInput(args []string) ([]batchCommand, error) {
	// Check if stdin has data (pipe mode)
	if stat, err := os.Stdin.Stat(); err == nil {
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, readErr := io.ReadAll(os.Stdin)
			if readErr != nil {
				return nil, fmt.Errorf("read stdin: %w", readErr)
			}
			if len(data) > 0 {
				var input batchInput
				if err := json.Unmarshal(data, &input); err != nil {
					return nil, fmt.Errorf("parse JSON input: %w", err)
				}
				// Validate types
				for i := range input.Commands {
					if input.Commands[i].Type == "" {
						input.Commands[i].Type = "exec"
					}
					if !validBatchTypes[input.Commands[i].Type] {
						return nil, fmt.Errorf("invalid type %q for command %d (must be exec/sql/redis/mongo)", input.Commands[i].Type, i)
					}
				}
				return input.Commands, nil
			}
		}
	}

	// Args mode: parse 'type:asset:command' or 'asset:command'
	if len(args) == 0 {
		return nil, nil
	}

	var commands []batchCommand
	for _, arg := range args {
		cmd, err := parseBatchArg(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid argument %q: %w", arg, err)
		}
		commands = append(commands, cmd)
	}
	return commands, nil
}

// parseBatchArg parses a single batch arg: 'type:asset:command' or 'asset:command'
func parseBatchArg(arg string) (batchCommand, error) {
	// Split on first ':'
	idx := strings.IndexByte(arg, ':')
	if idx < 0 {
		return batchCommand{}, fmt.Errorf("expected format 'asset:command' or 'type:asset:command'")
	}

	first := arg[:idx]
	rest := arg[idx+1:]

	// Check if first part is a known type
	if validBatchTypes[first] {
		// 'type:asset:command' — split rest on first ':'
		idx2 := strings.IndexByte(rest, ':')
		if idx2 < 0 {
			return batchCommand{}, fmt.Errorf("expected format 'type:asset:command'")
		}
		return batchCommand{
			Type:    first,
			Asset:   rest[:idx2],
			Command: rest[idx2+1:],
		}, nil
	}

	// 'asset:command' — default type exec
	return batchCommand{
		Type:    "exec",
		Asset:   first,
		Command: rest,
	}, nil
}

func newSessionID() string {
	return fmt.Sprintf("batch_%d", time.Now().UnixNano())
}

// batchAuditTool is the tool name every batch item is dispatched to and audited under.
// It used to be a cmdType→name map (exec / exec_sql / exec_redis / exec_mongo); those
// per-type tools are gone and the unified exec tool dispatches on the asset's real type,
// so there is exactly one name left. The batch item's own `type` field is still recorded
// in the JSON output and still selects the policy group for the pre-check
// (batchApprovalAssetType), so nothing is lost by collapsing this.
const batchAuditTool = "exec"

func printBatchUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] batch [args...]

Executes multiple commands in parallel with a single approval request.
Dispatches every item by its asset's real type (database, redis, mongodb,
etcd, kafka, k8s, ...) — not just ssh. The optional type prefix (sql, redis,
mongo) is now an assertion, not a dispatch selector: it fails that one item
fast if the asset isn't actually that type. A bare 'asset:command' entry
(no prefix) makes no assertion and runs against any asset type.

Input Modes:
  Stdin JSON (AI-friendly):
    echo '{"commands":[
      {"asset":"web-01","type":"exec","command":"uptime"},
      {"asset":"db-01","type":"sql","command":"SELECT 1"},
      {"asset":"cache","type":"redis","command":"PING"}
    ]}' | opsctl batch

  Positional Args:
    opsctl batch 'web-01:uptime' 'db-01:hostname'
    opsctl batch 'sql:db-01:SELECT 1' 'redis:cache:PING' 'web-01:uptime'

    Format: 'asset:command' (no assertion) or 'type:asset:command' (sql/redis/
    mongo assert the asset's real type; exec is a no-op assertion, same as
    no prefix)

Output:
  JSON with per-command results:
    {"results":[{"asset_id":1,"asset_name":"web-01","type":"exec",
      "command":"uptime","exit_code":0,"stdout":"...","stderr":""}]}

  Exit code: 0 if any command succeeded, 1 if all failed.

Examples:
  opsctl batch '1:uptime' '2:hostname'
  opsctl batch 'sql:prod-db:SELECT COUNT(*) FROM users' 'redis:cache:INFO'
  echo '{"commands":[{"asset":"1","command":"uptime"}]}' | opsctl batch
`)
}
