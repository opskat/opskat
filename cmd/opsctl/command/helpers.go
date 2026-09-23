package command

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// parseExecArgs 解析 opsctl exec <asset> 之后的参数：[--type <t>] [--scope <s>] [--] <command>。
// 选项只出现在命令开始之前——遇到 "--" 或第一个非选项 token 即进入命令，其后一切
// 原样属于远端命令（find / --type f 里的 --type 不是 opsctl 的，--scope 同理）。命令之前的
// 未知选项报错，不能静默丢弃，也不能拼进远端命令。全局 flag 已由 hoistGlobalFlags 取走。
//
// scope 语义与 AI exec 工具的 scope 参数一致（内部 ai/tool/tools_unified.go 的 schema
// 描述）：单机/哨兵资产是库号（缺省用资产配置的库），集群资产是无 key 命令必须指定的
// 节点 host:port。仅对 redis 资产有意义——cmdExec 在解析后用 validateRedisScope 对非
// redis 资产报错，而不是这里静默忽略或直接执行。
func parseExecArgs(args []string) (declaredType, scope, command string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			command = strings.Join(args[i+1:], " ")
		case arg == "--type":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("--type requires a value")
			}
			declaredType = args[i+1] //nolint:gosec // guarded by the i+1 >= len(args) check above
			i++
			continue
		case strings.HasPrefix(arg, "--type="):
			declaredType = strings.TrimPrefix(arg, "--type=")
			continue
		case arg == "--scope":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("--scope requires a value")
			}
			scope = args[i+1] //nolint:gosec // guarded by the i+1 >= len(args) check above
			i++
			continue
		case strings.HasPrefix(arg, "--scope="):
			scope = strings.TrimPrefix(arg, "--scope=")
			continue
		case strings.HasPrefix(arg, "-"):
			return "", "", "", fmt.Errorf("unknown flag %s (put the remote command after --)", arg)
		default:
			command = strings.Join(args[i:], " ")
		}
		break
	}
	if command == "" {
		return "", "", "", fmt.Errorf("no command given")
	}
	return declaredType, scope, command, nil
}

// validateRedisScope 校验 --scope（opsctl exec）/ batch 条目的 scope 字段只用在 redis
// 资产上：语义（库号 vs 集群节点 host:port）是 redis 命令路由独有的（helper.
// ExecRedisOnAsset / RedisNodeRequiredError），对其它资产类型给 scope 静默忽略会让用户
// 误以为它生效了；报错退出码 1 而不是让 exec/batch 的通用参数解析承担这条资产类型专属
// 规则——保持共享代码不按类型字符串分支（AGENTS.md OCP），只在这一处调用
// asset.IsRedis() 这个既有的实体谓词。
func validateRedisScope(asset *asset_entity.Asset, scope string) error {
	if scope == "" || asset.IsRedis() {
		return nil
	}
	return fmt.Errorf("--scope is only meaningful for redis assets; asset %q is type=%s", asset.Name, asset.Type)
}

// parseRemotePath parses numeric assetID:path strings without repository lookup.
func parseRemotePath(s string) (int64, string) {
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return 0, s
	}
	id, err := strconv.ParseInt(s[:idx], 10, 64)
	if err != nil {
		return 0, s
	}
	return id, s[idx+1:]
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// prepareExecCommand runs the side-effect-free gates a command must clear before any
// approval step touches it — executor lookup, canonicalize, precheck, in that order.
// It mirrors internal/ai/tool/tool_handlers_unified.go's handleExec steps 4/6/7 (see that
// function's doc comment for the full ordering rationale): opsctl used to run only the
// --type assertion before requireApproval, so an asset type with no executor at all
// (rdp/vnc/oss/local), a malformed command that canonicalize would reject (kafka/etcd
// DSL parse errors, k8s with no kubeconfig), or an unmet precondition (serial: no active
// session) all surfaced only *after* the user had already been shown — and had to answer
// — a desktop approval dialog for a command that could never have run.
//
// Returns the command to use for policy matching AND approval display: the canonical
// form if the asset's type registered a CanonicalizeFunc, the original command
// otherwise. The caller must still dispatch execution with the original, raw command —
// see cmdExec's doc comment (step 9) for why feeding the canonical form to the executor
// is lossy.
func prepareExecCommand(ctx context.Context, asset *asset_entity.Asset, command string) (string, error) {
	if _, ok := permission.ExecutorFor(asset.Type); !ok {
		return "", unsupportedExecTypeError(asset)
	}

	checkCommand := command
	if canonicalize, ok := permission.CanonicalizeFor(asset.Type); ok {
		canonical, err := canonicalize(asset, command)
		if err != nil {
			return "", err
		}
		checkCommand = canonical
	}

	if precheck, ok := permission.PrecheckFor(asset.Type); ok {
		if err := precheck(ctx, asset); err != nil {
			return "", err
		}
	}

	return checkCommand, nil
}

// unsupportedExecTypeError mirrors internal/ai/tool/tool_handlers_unified.go's
// unsupportedTypeError verbatim, so the same asset type reports the same message
// whether the command came in through opsctl or the AI exec tool.
func unsupportedExecTypeError(asset *asset_entity.Asset) error {
	return fmt.Errorf("asset %q (type=%s) has no exec support yet; supported types: %s",
		asset.Name, asset.Type, strings.Join(permission.RegisteredExecTypes(), ", "))
}

// rejectExtraArgs 报告子命令消费不了的多余参数并返回 true。静默忽略会让写错位置的
// flag（--delete-assets、--mfa-code 等）悄悄失效。
func rejectExtraArgs(extra []string) bool {
	if len(extra) == 0 {
		return false
	}
	fmt.Fprintf(os.Stderr, "Error: unexpected argument(s): %s\n", strings.Join(extra, " "))
	return true
}
