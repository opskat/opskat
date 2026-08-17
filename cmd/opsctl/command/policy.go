package command

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
)

// opsctl policy 家族（spec「Rule management: opsctl policy」）：show 只读免 TTY；
// allow / deny / rm 只在交互式终端中运行，非交互以退出码 3 + NEEDS TTY 拒绝且不落
// 任何改动——这是"AI 不能给自己扩权"的唯一执行点。group 子族与 attach / detach
// （policy_group.go）沿用同一门禁与回显/确认/审计骨架。

// policyBroaderMark 是"结果比请求的主体更宽"的标注（决策 12）的英文文案，中文经
// PolicyMsg 给出；它给人读，随 locale。
const policyBroaderMark = "(broader than the requested subject)"

// policyConfirmStreams 是永久写入二次确认的输入输出来源：回显写 out、决策读 in。
// 变量化以便测试注入缓冲。
var policyConfirmStreams = func() (io.Reader, io.Writer) { return os.Stdin, os.Stderr }

func cmdPolicy(ctx context.Context, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPolicyUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	switch args[0] {
	case "show":
		return cmdPolicyShow(ctx, args[1:], session)
	case "allow":
		return cmdPolicyWrite(ctx, permission.RuleAllow, args[1:], session)
	case "deny":
		return cmdPolicyWrite(ctx, permission.RuleDeny, args[1:], session)
	case "rm":
		return cmdPolicyRm(ctx, args[1:], session)
	case "group":
		return cmdPolicyGroup(ctx, args[1:], session)
	case "attach":
		return cmdPolicyAttachDetach(ctx, true, args[1:])
	case "detach":
		return cmdPolicyAttachDetach(ctx, false, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown policy subcommand %q\n\nRun 'opsctl policy --help' for usage.\n", args[0])
		return 1
	}
}

func printPolicyUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl policy show  <asset> | --group <group>
  opsctl policy allow <asset>... | --group <group>...  [--type <asset-type>] -- <pattern>...
  opsctl policy deny  <asset>... | --group <group>...  [--type <asset-type>] -- <pattern>...
  opsctl policy rm    <asset>  | --group <group>  <id>
  opsctl policy group list   [--type <policy-type>]
  opsctl policy group show   <group-id>
  opsctl policy group create --name <name> --type <policy-type>
  opsctl policy group copy   <group-id> --name <name>
  opsctl policy group allow  <group-id> -- <pattern>...
  opsctl policy group deny   <group-id> -- <pattern>...
  opsctl policy group rm     <group-id> [<entry-id>]
  opsctl policy attach <asset> | --group <group>  <group-id>...
  opsctl policy detach <asset> | --group <group>  <group-id>...

Subcommands:
  show    Read-only view of the effective rules (no TTY needed). For an asset it
          merges the asset, its group chain and referenced policy groups, marks
          allow rules shadowed by a deny, and lists still-valid grants. For a
          group it lists the group's own policy columns.
  allow   Write permanent allow rules. Interactive terminal only (exit 3 +
          NEEDS TTY otherwise). Echoes the rules and asks for confirmation.
  deny    Write permanent deny rules, same gating as allow.
  rm      Remove one entry listed by show: the target's own allow/deny rule or
          a grant item (g<id>). Interactive terminal only.
  group   Manage policy groups (list/show/create/copy/allow/deny/rm); list and
          show are read-only, the rest are interactive only. Run
          'opsctl policy group --help' for details.
  attach  Attach policy groups to an asset or asset group. A group whose type
          does not match the target fails before any write. Interactive only.
  detach  Remove policy-group references from an asset or asset group.
          Interactive only.

Options:
  --type <asset-type>  On an asset target: a type assertion (must match the
                       asset's type). On a group target: REQUIRED -- a group
                       has no type, this selects which policy shape the rules
                       land in. For 'policy group list/create' it is the policy
                       type (command/query/redis/mongo/kafka/k8s/etcd/oss).
  --group <group>      Target an asset group instead of assets (repeatable).

Examples:
  opsctl policy show web-01
  opsctl policy show --group production
  opsctl policy allow web-01 -- 'systemctl restart nginx' 'df -h'
  opsctl policy allow --group production --type ssh -- 'uptime'
  opsctl policy deny web-01 -- 'rm -rf *'
  opsctl policy rm web-01 2
  opsctl policy rm web-01 g12
  opsctl policy group list --type query
  opsctl policy group copy builtin:linux-readonly --name my-readonly
  opsctl policy group allow 5 -- 'uptime'
  opsctl policy group rm 5 3
  opsctl policy attach web-01 builtin:linux-readonly
  opsctl policy attach --group production builtin:sql-readonly
`)
}

// --- 目标解析 ---

// policyWriteTarget 是一次写入的已解析目标：资产或组 + 该目标的 canonical 类型 +
// 按该类型归一化后的 pattern。
type policyWriteTarget struct {
	asset     *asset_entity.Asset
	group     *group_entity.Group
	canonical string
	patterns  []string
	landed    []permission.LandedRule
}

// holder 返回目标背后的策略持有者（资产或组）。
func (t *policyWriteTarget) holder() policyent.Holder {
	if t.asset != nil {
		return t.asset
	}
	return t.group
}

func (t *policyWriteTarget) label() string {
	if t.asset != nil {
		return fmt.Sprintf("asset %s (ID %d)", t.asset.Name, t.asset.ID)
	}
	return fmt.Sprintf("group %s (ID %d)", t.group.Name, t.group.ID)
}

// cliRef 渲染目标在 CLI 上的引用形式（供报错里的出路命令使用，恒定 ASCII）。
func (t *policyWriteTarget) cliRef() string {
	if t.asset != nil {
		return strconv.FormatInt(t.asset.ID, 10)
	}
	return "--group " + strconv.FormatInt(t.group.ID, 10)
}

// splitPolicyArgs 把原始参数按首个 "--" 切成 flag+目标 与 pattern。切分发生在 flag
// 解析**之前**：flag.FlagSet 的 Parse 会吞掉它遇到的 "--"，等解析完就分不出"用户
// 忘了 --"与"flag 包吃掉了"。"--" 之后的一切都是 pattern，不再属于 opsctl。
func splitPolicyArgs(args []string) (before, patterns []string, err error) {
	for i, a := range args {
		if a == "--" {
			if len(args[i+1:]) == 0 {
				return nil, nil, errors.New("no pattern given after '--'")
			}
			return args[:i], args[i+1:], nil
		}
	}
	return nil, nil, errors.New("patterns must follow '--' (e.g. opsctl policy allow web-01 -- 'uptime')")
}

// parsePolicyWriteFlags 手工扫描 "--" 之前的 flag（--type / 可重复的 --group）。
// 不用 flag.FlagSet：它在第一个非 flag 参数处停止解析，而 spec 的用法把 --type 放在
// 资产名之后（opsctl policy allow <asset> --type <t> -- <pattern>），与 extractTypeFlag
// 处理 opsctl exec 的 "--" 之前的 --type 是同一条约定。未知 flag 报错。
func parsePolicyWriteFlags(before []string) (declared string, groups, targets []string, err error) {
	for i := 0; i < len(before); i++ {
		arg := before[i]
		var name, value string
		var hasValue, inline bool
		if eq := strings.Index(arg, "="); eq >= 0 && strings.HasPrefix(arg, "--") {
			name, value, hasValue, inline = arg[:eq], arg[eq+1:], true, true
		} else if i+1 < len(before) {
			name, value, hasValue = arg, before[i+1], true
		} else {
			name = arg
		}
		skipValue := !inline
		switch name {
		case "--type":
			if !hasValue {
				return "", nil, nil, fmt.Errorf("--type requires a value")
			}
			declared = value
			if skipValue {
				i++
			}
		case "--group":
			if !hasValue {
				return "", nil, nil, fmt.Errorf("--group requires a value")
			}
			groups = append(groups, value)
			if skipValue {
				i++
			}
		default:
			if strings.HasPrefix(arg, "--") {
				return "", nil, nil, fmt.Errorf("unknown flag %s", arg)
			}
			targets = append(targets, arg)
		}
	}
	return declared, groups, targets, nil
}

// resolvePolicyTargets 解析资产与组目标并归一化 pattern。全部校验发生在写入之前：
// 目标不存在、类型断言不符、组目标缺 --type、归一化为空都在这里失败。
func resolvePolicyTargets(ctx context.Context, assetNames, groupNames []string, declared string, rawPatterns []string) ([]policyWriteTarget, error) {
	if len(assetNames) == 0 && len(groupNames) == 0 {
		return nil, errors.New("no target: pass assets and/or --group")
	}
	if len(rawPatterns) == 0 {
		return nil, errors.New("no pattern given after '--'")
	}

	var targets []policyWriteTarget
	for _, name := range assetNames {
		asset, err := resolveAsset(ctx, name)
		if err != nil {
			return nil, err
		}
		// 资产目标的 --type 沿用既有断言语义：规则形状由资产自身的类型决定。
		if err := permission.AssertAssetType(asset, declared); err != nil {
			return nil, err
		}
		targets = append(targets, policyWriteTarget{asset: asset, canonical: asset.Type})
	}
	for _, name := range groupNames {
		gid, _, err := resolveGroup(ctx, name)
		if err != nil {
			return nil, err
		}
		group, err := group_repo.Group().Find(ctx, gid)
		if err != nil {
			return nil, fmt.Errorf("group not found: ID %d", gid)
		}
		// 组没有类型，--type 是选规则形状的唯一依据，缺失即失败，绝不猜默认形状。
		if declared == "" {
			return nil, fmt.Errorf("--type is required for group targets: a group has no type, --type selects which policy shape the rules land in (e.g. --type %s)", asset_entity.AssetTypeSSH)
		}
		canonical, ok := permission.CanonicalTypeFor(declared)
		if !ok {
			return nil, fmt.Errorf("unknown type %q for --type", declared)
		}
		if !permission.TypeRulesSupported(canonical) {
			return nil, fmt.Errorf("type %q has no permanent-rule shape; file transfers need a direction: use --type cp:read or --type cp:write", declared)
		}
		targets = append(targets, policyWriteTarget{group: group, canonical: canonical})
	}

	for i := range targets {
		normalized := make([]string, 0, len(rawPatterns))
		for _, p := range rawPatterns {
			norms := permission.NormalizeGrantPatterns(targets[i].canonical, p, permission.GrantOriginUser)
			if len(norms) == 0 {
				// 归一化为空是一个答案：什么都不落并报错（OSS 的目录标记场景），
				// 不退回原串。
				return nil, fmt.Errorf("pattern %q normalizes to nothing on type %s; nothing would be written", p, targets[i].canonical)
			}
			normalized = append(normalized, norms...)
		}
		targets[i].patterns = normalized
	}
	return targets, nil
}

// --- 写入路径（allow / deny / 终端"永久允许"共用） ---

func cmdPolicyWrite(ctx context.Context, side permission.RuleSide, args []string, _ string) int {
	name := "policy allow"
	if side == permission.RuleDeny {
		name = "policy deny"
	}
	before, rawPatterns, err := splitPolicyArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	declared, groupNames, assetNames, err := parsePolicyWriteFlags(before)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// TTY 门禁（spec 决策 14）：写类子命令只在交互式终端运行，非交互拒绝且不落改动。
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, name)
	}

	targets, err := resolvePolicyTargets(ctx, assetNames, groupNames, declared, rawPatterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := writePermanentRules(ctx, side, targets); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// refusePolicyWrite 是 policy 写类子命令的结构化拒绝：退出码 3 + stderr 首行
// NEEDS TTY（恒定英文 ASCII），正文随 locale。
func refusePolicyWrite(ctx context.Context, name string) int {
	refusal := &structuredRefusal{
		marker: needsTTYMarker,
		body: policy.PolicyFmt(ctx,
			"%s must run in an interactive terminal: a permanent rule can only be made effective by a human. Run it yourself in your terminal.",
			"%s 必须在交互式终端中运行：永久规则只能由人让它生效。请在你自己的终端里执行。", name),
	}
	logger.Ctx(ctx).Warn("opsctl policy write refused: no interactive terminal", zap.String("command", name))
	return writeApprovalFailure(os.Stderr, refusal)
}

// writePermanentRules 是永久规则的唯一写入路径（spec 决策 13：终端提示的"永久允许"
// 与显式 opsctl policy allow/deny 共用）。顺序：内存落点 → 遮蔽检测 → 回显与二次
// 确认 → 一次事务内的读-改-写 → 审计。任何一步失败都不落任何改动。
func writePermanentRules(ctx context.Context, side permission.RuleSide, targets []policyWriteTarget) error {
	log := logger.Ctx(ctx)
	log.Info("opsctl policy rule write started",
		zap.String("side", ruleSideName(side)), zap.Int("targets", len(targets)))

	for i := range targets {
		landed, err := permission.AppendTypeRules(targets[i].holder(), targets[i].canonical, side, targets[i].patterns)
		if err != nil {
			return fmt.Errorf("%s: %w", targets[i].label(), err)
		}
		targets[i].landed = landed
	}

	// 决策 19：allow 被生效中的 deny 遮蔽时拒绝写入——被遮蔽的 allow 自始无效。
	if side == permission.RuleAllow {
		for i := range targets {
			sh, err := shadowingDenyFor(ctx, &targets[i])
			if err != nil {
				return err
			}
			if sh != nil {
				log.Warn("opsctl policy rule write blocked by shadowing deny",
					zap.String("target", targets[i].label()), zap.String("deny", sh.Rule))
				return shadowRefusal(ctx, &targets[i], sh)
			}
		}
	}

	if !confirmRuleWrite(ctx, side, targets) {
		log.Info("opsctl policy rule write declined at confirmation",
			zap.String("side", ruleSideName(side)), zap.Int("targets", len(targets)))
		return errors.New("declined: nothing written")
	}

	// 一次事务内的读-改-写：多目标/多 pattern 全或无。
	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		for i := range targets {
			if err := rewriteTargetRules(txCtx, side, &targets[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Error("opsctl policy rule write failed", zap.Error(err))
		return err
	}

	for i := range targets {
		writeRuleAudit(ctx, side, &targets[i])
	}
	log.Info("opsctl policy rule write completed",
		zap.String("side", ruleSideName(side)), zap.Int("targets", len(targets)))
	return nil
}

// rewriteTargetRules 在事务内重读 holder、重放落点并落库。事务内的重读保证读-改-写
// 不覆盖并发改动；pattern 与落法都是确定性的，重放与回显一致。
func rewriteTargetRules(ctx context.Context, side permission.RuleSide, t *policyWriteTarget) error {
	if t.asset != nil {
		fresh, err := asset_repo.Asset().Find(ctx, t.asset.ID)
		if err != nil {
			return err
		}
		if _, err := permission.AppendTypeRules(fresh, t.canonical, side, t.patterns); err != nil {
			return err
		}
		return asset_repo.Asset().Update(ctx, fresh)
	}
	fresh, err := group_repo.Group().Find(ctx, t.group.ID)
	if err != nil {
		return err
	}
	if _, err := permission.AppendTypeRules(fresh, t.canonical, side, t.patterns); err != nil {
		return err
	}
	return group_repo.Group().Update(ctx, fresh)
}

// shadowingDenyFor 收集该目标的生效规则并返回第一条遮蔽者。
func shadowingDenyFor(ctx context.Context, t *policyWriteTarget) (*permission.SourcedRule, error) {
	var view *permission.TypeRuleView
	var err error
	if t.asset != nil {
		view, err = permission.CollectTypeRules(ctx, t.asset, t.canonical)
	} else {
		view, err = permission.CollectHolderTypeRules(ctx, t.group, t.canonical)
	}
	if err != nil {
		return nil, err
	}
	for _, l := range t.landed {
		if sh := permission.ShadowingDeny(view, t.canonical, l.Rule); sh != nil {
			return sh, nil
		}
	}
	return nil, nil
}

// sourcedRuleOrigin 渲染一条来源层（资产 / 组链上的组 / 权限组含内置组名）。
func sourcedRuleOrigin(ctx context.Context, r permission.SourcedRule) string {
	switch r.Kind {
	case permission.RuleSourceAsset:
		return policy.PolicyFmt(ctx, "asset %s (ID %d)", "资产 %s（ID %d）", r.HolderName, r.HolderID)
	case permission.RuleSourceGroup:
		return policy.PolicyFmt(ctx, "group %s (ID %d)", "资产组 %s（ID %d）", r.HolderName, r.HolderID)
	default:
		return policy.PolicyFmt(ctx, "policy group %s (%s)", "权限组 %s（%s）", r.PolicyGroupName, r.PolicyGroupID)
	}
}

// shadowRefusal 构造遮蔽拒绝：点名 deny 原文、来源层，并给出出路的命令原文
// （spec 决策 19；权限组里的遮蔽走 copy → 改副本 → detach/attach，恒定英文 ASCII）。
func shadowRefusal(ctx context.Context, t *policyWriteTarget, sh *permission.SourcedRule) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
		"refusing to write: this allow rule would never take effect, it is shadowed by a deny",
		"拒绝写入：这条 allow 规则永远不会生效，它被一条 deny 遮蔽"))
	fmt.Fprintf(&sb, "%s %s\n", policy.PolicyMsg(ctx, "deny rule:", "deny 规则："), sh.Rule)
	fmt.Fprintf(&sb, "%s %s\n", policy.PolicyMsg(ctx, "source:", "来源："), sourcedRuleOrigin(ctx, *sh))
	if sh.Kind == permission.RuleSourcePolicyGroup {
		fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
			"to fix: copy the policy group, remove the deny on the copy, then swap it on the target:",
			"出路：复制该权限组、在副本上删掉这条 deny，再把目标挂的组换成副本："))
		fmt.Fprintf(&sb, "  opsctl policy group copy %s --name <new-name>\n", sh.PolicyGroupID)
		sb.WriteString("  opsctl policy group show <copy-id>   # find the deny entry id\n")
		sb.WriteString("  opsctl policy group rm <copy-id> <entry-id>\n")
		fmt.Fprintf(&sb, "  opsctl policy detach %s %s\n", t.cliRef(), sh.PolicyGroupID)
		sb.WriteString("  opsctl policy attach " + t.cliRef() + " <copy-id>\n")
	} else {
		fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
			"to fix: remove the deny rule first (find the entry id via show):",
			"出路：先撤掉这条 deny（编号用 show 查）："))
		fmt.Fprintf(&sb, "  opsctl policy show %s\n", t.cliRef())
		fmt.Fprintf(&sb, "  opsctl policy rm %s <entry-id>\n", t.cliRef())
	}
	return errors.New(strings.TrimRight(sb.String(), "\n"))
}

// landedEcho 是回显的一组落点：目标标签 + canonical 类型 + 实际落的规则（决策 12 的
// Broader 标注在这里渲染）。
type landedEcho struct {
	label     string
	canonical string
	landed    []permission.LandedRule
}

// askRuleConfirm 打印 prompt 并读一行；y/yes 之外（含空输入与 EOF）一律不写。
func askRuleConfirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s ", prompt) //nolint:errcheck // 终端呈现尽力而为
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.TrimSpace(line) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// confirmLandedRules 回显将要写入的规则原文并二次确认（决策 12）；结果比请求主体更宽
// 时明确标注。policy allow/deny 与 policy group allow/deny 共用。
func confirmLandedRules(ctx context.Context, sideWord string, groups []landedEcho) bool {
	in, out := policyConfirmStreams()
	fmt.Fprintf(out, "%s\n", policy.PolicyMsg(ctx, "rules to be written:", "将要写入的规则：")) //nolint:errcheck // 终端呈现尽力而为
	for _, g := range groups {
		fmt.Fprintf(out, "%s (%s):\n", g.label, g.canonical) //nolint:errcheck // 终端呈现尽力而为
		for _, l := range g.landed {
			if l.Broader {
				fmt.Fprintf(out, "  %s %s %s\n", sideWord, l.Rule, policy.PolicyMsg(ctx, policyBroaderMark, "（比请求的主体更宽）")) //nolint:errcheck // 终端呈现尽力而为
			} else {
				fmt.Fprintf(out, "  %s %s\n", sideWord, l.Rule) //nolint:errcheck // 终端呈现尽力而为
			}
		}
	}
	return askRuleConfirm(in, out, policy.PolicyMsg(ctx, "write these rules? [y/N]", "写入这些规则？[y/N]"))
}

// confirmRuleWrite 是资产/组目标的回显确认薄适配：目标标签 + canonical + 落点。
func confirmRuleWrite(ctx context.Context, side permission.RuleSide, targets []policyWriteTarget) bool {
	groups := make([]landedEcho, 0, len(targets))
	for _, t := range targets {
		groups = append(groups, landedEcho{label: t.label(), canonical: t.canonical, landed: t.landed})
	}
	return confirmLandedRules(ctx, ruleSideName(side), groups)
}

func ruleSideName(side permission.RuleSide) string {
	if side == permission.RuleDeny {
		return "deny"
	}
	return "allow"
}

// writeRuleAudit 为一次成功的永久规则写入单独记一行审计（spec Security）。
func writeRuleAudit(ctx context.Context, side permission.RuleSide, t *policyWriteTarget) {
	rules := make([]string, 0, len(t.landed))
	for _, l := range t.landed {
		rules = append(rules, l.Rule)
	}
	args := map[string]any{
		"side":     ruleSideName(side),
		"type":     t.canonical,
		"patterns": t.patterns,
		"rules":    rules,
	}
	if t.asset != nil {
		args["asset_id"] = t.asset.ID
		args["asset"] = t.asset.Name
	} else {
		args["group_id"] = t.group.ID
		args["group"] = t.group.Name
	}
	argsJSON, _ := json.Marshal(args)
	decision := &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
	if side == permission.RuleDeny {
		decision = &aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}
	}
	writeOpsctlAudit(ctx, "policy_rule", string(argsJSON), ruleSideName(side), nil, decision)
}

// --- 终端"永久允许"接缝（决策 13）：与 policy allow 同一条写入路径 ---

// writeAllowAlwaysRuleImpl 接通 tty_approval.go 的接缝：patterns 已由调用方按
// GrantOriginSystem 归一化；形状由资产自身的类型决定。cp 面不带方向，无法落一条
// 不撒谎的永久规则，如实失败（fail-closed），把方向化的命令交给人（face 进 pattern：
// 资产目标的 --type 是类型断言，cp 面不是资产类型，走 --type 会被拒收）。
func writeAllowAlwaysRuleImpl(ctx context.Context, assetID int64, approvalType string, patterns []string) error {
	if approvalType == permission.GrantToolCp {
		return errors.New(policy.PolicyMsg(ctx,
			"a permanent cp rule needs a direction this approval does not carry; choose allow once or deny, or run: opsctl policy allow <id> -- 'cp:read:<path>'",
			"永久 cp 规则需要方向，而这次审批没有带；请选本次允许或拒绝，或执行：opsctl policy allow <id> -- 'cp:read:<路径>'"))
	}
	asset, err := asset_repo.Asset().Find(ctx, assetID)
	if err != nil {
		return err
	}
	if !permission.TypeRulesSupported(asset.Type) {
		return fmt.Errorf("asset %q (type %s) has no permanent-rule support", asset.Name, asset.Type)
	}
	targets := []policyWriteTarget{{asset: asset, canonical: asset.Type, patterns: patterns}}
	return writePermanentRules(ctx, permission.RuleAllow, targets)
}

func init() {
	writeAllowAlwaysRule = writeAllowAlwaysRuleImpl
}

// --- show ---

// policyEntry 是 show 的一行：编号、侧、规则原文、来源与可撤性。show 与 rm 用同一
// 个构造函数产出条目，编号因此稳定。
type policyEntry struct {
	id        string
	side      permission.RuleSide
	src       permission.SourcedRule
	shape     string // 组目标：条目所在的形状列（policy kind）
	own       bool   // 是否是目标自身那一列（可由 rm 撤）
	shadow    *permission.SourcedRule
	grant     *grant_entity.GrantItem
	remaining time.Duration // grant 条目的剩余有效时间
}

func cmdPolicyShow(ctx context.Context, args []string, session string) int {
	fs := flag.NewFlagSet("policy show", flag.ContinueOnError)
	var groupFlags stringSliceFlag
	fs.Var(&groupFlags, "group", "Show this group's own policy columns")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()

	if len(groupFlags) > 0 {
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "Error: pass either --group or an asset, not both\n")
			return 1
		}
		return cmdPolicyShowGroup(ctx, groupFlags)
	}
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "Error: policy show takes exactly one asset (or --group <group>)\n")
		return 1
	}
	asset, err := resolveAsset(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	entries, view, err := assetPolicyEntries(ctx, asset, session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	renderPolicyEntries(ctx, assetIdentity(asset.Name, asset.ID, asset.Type), entries)
	if len(view.Groups) > 0 {
		fmt.Fprintf(os.Stdout, "\n%s\n", policy.PolicyMsg(ctx, "referenced policy groups:", "引用的权限组：")) //nolint:errcheck // 终端呈现尽力而为
		for _, g := range view.Groups {
			fmt.Fprintf(os.Stdout, "  %s (%s)\n", g.Name, g.ID) //nolint:errcheck // 终端呈现尽力而为
		}
	}
	return 0
}

// assetPolicyEntries 构造资产视图的全部条目：合并生效的 allow/deny（语义与
// policyHoldersForAsset 判定路径同源、每条标来源层）+ 仍有效的 grant。
func assetPolicyEntries(ctx context.Context, asset *asset_entity.Asset, session string) ([]policyEntry, *permission.TypeRuleView, error) {
	view, err := permission.CollectTypeRules(ctx, asset, asset.Type)
	if err != nil {
		return nil, nil, err
	}
	var entries []policyEntry
	next := 1
	for _, r := range view.Allow {
		e := policyEntry{id: strconv.Itoa(next), side: permission.RuleAllow, src: r,
			own: r.Kind == permission.RuleSourceAsset && !r.Generic}
		e.shadow = permission.ShadowingDeny(view, asset.Type, r.Rule)
		entries = append(entries, e)
		next++
	}
	for _, r := range view.Deny {
		entries = append(entries, policyEntry{id: strconv.Itoa(next), side: permission.RuleDeny, src: r,
			own: r.Kind == permission.RuleSourceAsset && !r.Generic})
		next++
	}

	// 仍有效的 grant 及剩余时间（按 session 创建时间起算 24 小时）。
	items, remaining := activeGrantItems(ctx, session)
	for _, item := range items {
		if !grantItemAppliesToAsset(ctx, item, asset) {
			continue
		}
		entries = append(entries, policyEntry{
			id: "g" + strconv.FormatInt(item.ID, 10), grant: item, remaining: remaining,
		})
	}
	return entries, view, nil
}

// activeGrantItems 列出当前 session 里仍有效的 grant items 及其剩余时间。
func activeGrantItems(ctx context.Context, session string) ([]*grant_entity.GrantItem, time.Duration) {
	if session == "" {
		return nil, 0
	}
	repo := grant_repo.Grant()
	if repo == nil {
		return nil, 0
	}
	items, err := repo.ListApprovedItems(ctx, session)
	if err != nil {
		logger.Ctx(ctx).Warn("list approved grant items", zap.String("sessionID", session), zap.Error(err))
		return nil, 0
	}
	sess, err := repo.GetSession(ctx, session)
	if err != nil || sess == nil {
		return nil, 0
	}
	remaining := sessionMaxAge - time.Since(time.Unix(sess.Createtime, 0))
	if remaining <= 0 {
		return nil, 0
	}
	return items, remaining
}

// grantItemAppliesToAsset 与 permission.grantItemMatchesTarget 同一判定：AssetID 精确、
// GroupID 按组链、两者皆 0 匹配所有资产。未导出故在此镜像，两边一起改。
func grantItemAppliesToAsset(ctx context.Context, item *grant_entity.GrantItem, asset *asset_entity.Asset) bool {
	if item.AssetID != 0 {
		return item.AssetID == asset.ID
	}
	if item.GroupID != 0 {
		for _, g := range policy.ResolveGroupChain(ctx, asset.GroupID) {
			if g.ID == item.GroupID {
				return true
			}
		}
		return false
	}
	return true
}

func cmdPolicyShowGroup(ctx context.Context, groupNames []string) int {
	for _, name := range groupNames {
		gid, _, err := resolveGroup(ctx, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		group, err := group_repo.Group().Find(ctx, gid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: group not found: ID %d\n", gid)
			return 1
		}
		entries, err := groupPolicyEntries(group)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		renderPolicyEntries(ctx, fmt.Sprintf("group %s (ID %d)", group.Name, group.ID), entries)
	}
	return 0
}

// groupPolicyEntries 列出组自身各形状列的规则，全部可由 rm 撤。
func groupPolicyEntries(group *group_entity.Group) ([]policyEntry, error) {
	var entries []policyEntry
	next := 1
	for _, shape := range permission.ListHolderRuleShapes(group) {
		for _, r := range shape.Allow {
			entries = append(entries, policyEntry{
				id: strconv.Itoa(next), side: permission.RuleAllow, shape: shape.PolicyType, own: true,
				src: permission.SourcedRule{Rule: r, Kind: permission.RuleSourceGroup, HolderID: group.ID, HolderName: group.Name},
			})
			next++
		}
		for _, r := range shape.Deny {
			entries = append(entries, policyEntry{
				id: strconv.Itoa(next), side: permission.RuleDeny, shape: shape.PolicyType, own: true,
				src: permission.SourcedRule{Rule: r, Kind: permission.RuleSourceGroup, HolderID: group.ID, HolderName: group.Name},
			})
			next++
		}
	}
	return entries, nil
}

// renderPolicyEntries 渲染条目：allow/deny 分节、每条标来源层；被遮蔽的 allow 标注；
// 权限组来源的条目注明不由 rm 撤并指向 detach / policy group 两条出路；grant 标剩余时间。
func renderPolicyEntries(ctx context.Context, title string, entries []policyEntry) {
	out := os.Stdout
	fmt.Fprintf(out, "%s\n\n", title) //nolint:errcheck // 终端呈现尽力而为

	printSection := func(header string, side permission.RuleSide) {
		var section []policyEntry
		for _, e := range entries {
			if e.grant == nil && e.side == side {
				section = append(section, e)
			}
		}
		if len(section) == 0 {
			return
		}
		fmt.Fprintf(out, "%s\n", header) //nolint:errcheck // 终端呈现尽力而为
		for _, e := range section {
			line := fmt.Sprintf("  #%-3s %s", e.id, e.src.Rule)
			shapeTag := ""
			if e.shape != "" {
				shapeTag = " [" + e.shape + "]"
			}
			fmt.Fprintf(out, "%s%s   (%s)\n", line, shapeTag, sourcedRuleOrigin(ctx, e.src)) //nolint:errcheck // 终端呈现尽力而为
			if e.shadow != nil {
				fmt.Fprintf(out, "       %s %s (%s)\n", policy.PolicyMsg(ctx, "shadowed by deny:", "被 deny 遮蔽："), e.shadow.Rule, sourcedRuleOrigin(ctx, *e.shadow)) //nolint:errcheck // 终端呈现尽力而为
			}
		}
		fmt.Fprintln(out) //nolint:errcheck // 终端呈现尽力而为
	}
	printSection(policy.PolicyMsg(ctx, "allow:", "allow："), permission.RuleAllow)
	printSection(policy.PolicyMsg(ctx, "deny:", "deny："), permission.RuleDeny)

	var grants []policyEntry
	for _, e := range entries {
		if e.grant != nil {
			grants = append(grants, e)
		}
	}
	if len(grants) > 0 {
		fmt.Fprintf(out, "%s\n", policy.PolicyMsg(ctx, "grants (removable via rm g<id>):", "grant 授权（用 rm g<编号> 撤销）：")) //nolint:errcheck // 终端呈现尽力而为
		for _, e := range grants {
			fmt.Fprintf(out, "  #%-3s %s   (%s)\n", e.id, e.grant.Command, //nolint:errcheck // 终端呈现尽力而为
				policy.PolicyFmt(ctx, "%dh left", "剩余 %d 小时", int(e.remaining.Hours())))
		}
		fmt.Fprintln(out) //nolint:errcheck // 终端呈现尽力而为
	}

	for _, e := range entries {
		if e.grant == nil && !e.own {
			fmt.Fprintf(out, "%s\n", policy.PolicyMsg(ctx, //nolint:errcheck // 终端呈现尽力而为
				"entries from other layers are not removable via rm: detach the policy group, or edit it via 'opsctl policy group' (builtin/extension groups: copy first)",
				"来自其他层的条目不由 rm 撤：用 policy detach 摘掉权限组，或经 'opsctl policy group' 修改（内置/扩展组需先 copy）"))
			break
		}
	}
}

// --- rm ---

func cmdPolicyRm(ctx context.Context, args []string, session string) int {
	fs := flag.NewFlagSet("policy rm", flag.ContinueOnError)
	var groupFlags stringSliceFlag
	fs.Var(&groupFlags, "group", "Remove from this group's own policy columns")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, "policy rm")
	}

	var err error
	switch {
	case len(groupFlags) == 1 && len(rest) == 1:
		err = rmGroupEntry(ctx, groupFlags[0], rest[0])
	case len(groupFlags) == 0 && len(rest) == 2:
		err = rmAssetEntry(ctx, rest[0], rest[1], session)
	default:
		fmt.Fprintf(os.Stderr, "Error: policy rm takes one target and one entry id (see 'opsctl policy show')\n")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// rmAssetEntry 撤资产目标的一条条目：自身的永久规则或 grant 授权。
func rmAssetEntry(ctx context.Context, targetName, entryID, session string) error {
	asset, err := resolveAsset(ctx, targetName)
	if err != nil {
		return err
	}
	entries, _, err := assetPolicyEntries(ctx, asset, session)
	if err != nil {
		return err
	}
	entry, ok := findPolicyEntry(entries, entryID)
	if !ok {
		return fmt.Errorf("no entry %s for this target; run 'opsctl policy show' to list entry ids", entryID)
	}
	if entry.grant != nil {
		return removeGrantItem(ctx, session, entry.grant.ID)
	}
	if !entry.own {
		return errors.New("this entry lives on another layer and is not removable via rm: detach the policy group, or edit it via 'opsctl policy group'")
	}
	logger.Ctx(ctx).Info("opsctl policy rm started", zap.Int64("assetID", asset.ID), zap.String("entry", entryID))
	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		fresh, err := asset_repo.Asset().Find(txCtx, asset.ID)
		if err != nil {
			return err
		}
		if err := permission.RemoveTypeRule(fresh, asset.Type, entry.side, entry.src.Rule); err != nil {
			return err
		}
		return asset_repo.Asset().Update(txCtx, fresh)
	}); err != nil {
		logger.Ctx(ctx).Error("opsctl policy rm failed", zap.Int64("assetID", asset.ID), zap.Error(err))
		return err
	}
	writeRmAudit(ctx, fmt.Sprintf("asset %s (ID %d)", asset.Name, asset.ID), entry.src.Rule)
	logger.Ctx(ctx).Info("opsctl policy rm completed", zap.Int64("assetID", asset.ID), zap.String("rule", entry.src.Rule))
	return nil
}

// rmGroupEntry 撤组目标自身某一形状列的一条规则。
func rmGroupEntry(ctx context.Context, groupName, entryID string) error {
	gid, _, err := resolveGroup(ctx, groupName)
	if err != nil {
		return err
	}
	group, err := group_repo.Group().Find(ctx, gid)
	if err != nil {
		return fmt.Errorf("group not found: ID %d", gid)
	}
	entries, err := groupPolicyEntries(group)
	if err != nil {
		return err
	}
	entry, ok := findPolicyEntry(entries, entryID)
	if !ok {
		return fmt.Errorf("no entry %s for this group; run 'opsctl policy show --group' to list entry ids", entryID)
	}
	logger.Ctx(ctx).Info("opsctl policy rm started", zap.Int64("groupID", gid), zap.String("entry", entryID))
	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		fresh, err := group_repo.Group().Find(txCtx, gid)
		if err != nil {
			return err
		}
		if err := permission.RemoveShapeRule(fresh, entry.shape, entry.side, entry.src.Rule); err != nil {
			return err
		}
		return group_repo.Group().Update(txCtx, fresh)
	}); err != nil {
		logger.Ctx(ctx).Error("opsctl policy rm failed", zap.Int64("groupID", gid), zap.Error(err))
		return err
	}
	writeRmAudit(ctx, fmt.Sprintf("group %s (ID %d)", group.Name, gid), entry.src.Rule)
	logger.Ctx(ctx).Info("opsctl policy rm completed", zap.Int64("groupID", gid), zap.String("rule", entry.src.Rule))
	return nil
}

// removeGrantItem 撤一条 grant：session 里剩余 items 经 UpdateItems 整体重写
// （repo 没有 per-item 删除；桌面端写下的存量行同样走这里）。
func removeGrantItem(ctx context.Context, session string, itemID int64) error {
	if session == "" {
		return errors.New("no active grant session")
	}
	repo := grant_repo.Grant()
	if repo == nil {
		return errors.New("grant repository unavailable")
	}
	items, err := repo.ListApprovedItems(ctx, session)
	if err != nil {
		return err
	}
	remaining := make([]*grant_entity.GrantItem, 0, len(items))
	var removed string
	for _, item := range items {
		if item.ID == itemID {
			removed = item.Command
			continue
		}
		remaining = append(remaining, item)
	}
	if removed == "" {
		return fmt.Errorf("grant item %d not found in the active session", itemID)
	}
	logger.Ctx(ctx).Info("opsctl policy rm started", zap.Int64("grantItemID", itemID))
	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		return repo.UpdateItems(txCtx, session, remaining)
	}); err != nil {
		logger.Ctx(ctx).Error("opsctl policy rm failed", zap.Int64("grantItemID", itemID), zap.Error(err))
		return err
	}
	writeRmAudit(ctx, "grant session "+session, removed)
	logger.Ctx(ctx).Info("opsctl policy rm completed", zap.Int64("grantItemID", itemID))
	return nil
}

func findPolicyEntry(entries []policyEntry, id string) (policyEntry, bool) {
	for _, e := range entries {
		if e.id == id {
			return e, true
		}
	}
	return policyEntry{}, false
}

func writeRmAudit(ctx context.Context, target, rule string) {
	argsJSON, _ := json.Marshal(map[string]any{"target": target, "rule": rule})
	writeOpsctlAudit(ctx, "policy_rule", string(argsJSON), "removed", nil,
		&aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny, MatchedPattern: rule})
}
