package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

func buildHandlerMap() map[string]tool.ToolHandlerFunc {
	m := make(map[string]tool.ToolHandlerFunc)
	for _, def := range tool.AllToolDefs() {
		m[def.Name] = def.Handler
	}
	return m
}

// refreshesDesktopUI reports whether a successful call to toolName changed asset or
// group data the desktop app's own UI caches — put_asset/put_group/delete_asset/
// delete_group all do. It is a named, directly-testable predicate rather than an
// inline `if` precisely because the inline form has no test coverage of its own: a
// change that mutates data through opsctl (e.g. delete_asset) but is missing from
// this list produces no failing test anywhere — the desktop just silently stops
// refreshing after that command. See TestRefreshesDesktopUI.
func refreshesDesktopUI(toolName string) bool {
	switch toolName {
	case "put_asset", "put_group", "delete_asset", "delete_group":
		return true
	default:
		return false
	}
}

func callHandler(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, toolName string, params map[string]any, decision ...*aictx.CheckResult) int {
	handler, ok := handlers[toolName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Internal error: unknown tool %s\n", toolName)
		return 1
	}

	if params == nil {
		params = map[string]any{}
	}

	ctx = aictx.WithAuditSource(ctx, "opsctl")

	var dec *aictx.CheckResult
	if len(decision) > 0 {
		dec = decision[0]
	}
	// 带着 ApprovalResult 进来，说明调用方已经走过 requireApproval（策略 / Grant /
	// 桌面审批）。handler 内部的权限检查是 fail-closed 的（permission.RequireChecker），
	// 而 opsctl 的 context 里没有 PolicyChecker——那是桌面 AI 会话专属的。这里显式声明
	// "已预检"，让 handler 跳过第二次检查，而不是靠"checker 为 nil 就放行"兜着。
	// 只对真的带了审批结论的调用生效：没预检过的（cp / list / create）拿不到豁免。
	if dec != nil {
		ctx = permission.WithPreapproved(ctx)
	}

	result, err := handler(ctx, params)

	// 写审计日志
	argsJSON, marshalErr := json.Marshal(params)
	if marshalErr != nil {
		logger.Default().Warn("marshal audit params", zap.Error(marshalErr))
	}
	writeOpsctlAudit(ctx, toolName, string(argsJSON), result, err, dec)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 写操作成功后通知桌面端刷新 UI
	if refreshesDesktopUI(toolName) {
		dataDir := bootstrap.ResolvedDataDir()
		token, tokenErr := bootstrap.ReadAuthToken(dataDir)
		if tokenErr != nil {
			logger.Default().Warn("read auth token", zap.Error(tokenErr))
		}
		approval.SendNotification(
			approval.SocketPath(dataDir),
			token,
			"asset",
		)
	}

	// Pretty-print JSON output
	var obj any
	if json.Unmarshal([]byte(result), &obj) == nil {
		pretty, err := json.MarshalIndent(obj, "", "  ")
		if err == nil {
			fmt.Println(string(pretty))
			return 0
		}
	}
	fmt.Println(result)
	return 0
}

// opsctlAuditWriter 全局审计写入器
var opsctlAuditWriter audit.AuditWriter = audit.NewDefaultAuditWriter()

// writeOpsctlAudit 统一的审计日志写入函数
func writeOpsctlAudit(ctx context.Context, toolName, argsJSON, result string, execErr error, decision *aictx.CheckResult) {
	opsctlAuditWriter.WriteToolCall(ctx, audit.ToolCallInfo{
		ToolName: toolName,
		ArgsJSON: argsJSON,
		Result:   result,
		Error:    execErr,
		Decision: decision,
	})
}
