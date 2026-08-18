package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/service/policy_group_svc"
	"github.com/opskat/opskat/internal/service/policy_rule_svc"
)

// opsctl policy group 子族与 policy attach / detach（spec 决策 21）。三种组 ID 形态的
// 可写性照搬服务层既有不变式：builtin:xxx 与 ext:xxx 只读（可 list/show/copy/
// attach/detach，不可 create/edit/rm），数字 ID 的用户组完全可写。拒绝理由是 CLI 本地
// 消息（服务层错误串是硬编码中文，只作兜底），出路命令恒定 ASCII。
//
// attach / detach 写 holder 策略列的 Groups 字段——与 allow/deny 同一列，只是字段
// 不同；列选择复用 T5 的注册知识（asset-kind 注册表 + k8s 列按 command 表解析）。

func cmdPolicyGroup(ctx context.Context, args []string, _ string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPolicyGroupUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	switch args[0] {
	case "list":
		return cmdPolicyGroupList(ctx, args[1:])
	case "show":
		return cmdPolicyGroupShow(ctx, args[1:])
	case "create":
		return cmdPolicyGroupCreate(ctx, args[1:])
	case "copy":
		return cmdPolicyGroupCopy(ctx, args[1:])
	case "allow":
		return cmdPolicyGroupWrite(ctx, permission.RuleAllow, args[1:])
	case "deny":
		return cmdPolicyGroupWrite(ctx, permission.RuleDeny, args[1:])
	case "rm":
		return cmdPolicyGroupRm(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown policy group subcommand %q\n\nRun 'opsctl policy group --help' for usage.\n", args[0])
		return 1
	}
}

func printPolicyGroupUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl policy group list   [--type <policy-type>]
  opsctl policy group show   <group-id>
  opsctl policy group create --name <name> --type <policy-type>
  opsctl policy group copy   <group-id> --name <name>
  opsctl policy group allow  <group-id> -- <pattern>...
  opsctl policy group deny   <group-id> -- <pattern>...
  opsctl policy group rm     <group-id> [<entry-id>]
  opsctl policy attach <asset> | --group <group>  <group-id>...
  opsctl policy detach <asset> | --group <group>  <group-id>...

Group IDs are builtin:<name>, ext:<name> or the numeric ID of a user group.
Builtin and extension groups are read-only: list/show/copy/attach/detach work,
create/edit/rm are refused with the copy-first route.

Subcommands:
  list    Read-only list of policy groups, optionally filtered by --type
          (no TTY needed).
  show    Read-only view of one group's allow/deny entries with stable
          numbering (no TTY needed).
  create  Create an empty user group. Interactive terminal only (exit 3 +
          NEEDS TTY otherwise).
  copy    Copy any group (builtin/extension included) into a new user group;
          --name is required. Interactive terminal only.
  allow   Write allow rules into a user group's own policy. Interactive
          terminal only. Echoes the rules and asks for confirmation.
  deny    Write deny rules, same gating as allow.
  rm      With <entry-id>: remove that one entry (ids come from show).
          Without: delete the whole user group. Interactive terminal only.
  attach/detach
          Add/remove policy-group references on an asset or asset group.
          A group whose type does not match the target fails before any
          write. Interactive terminal only.
`)
}

// --- flag 解析 ---

// parsePolicyGroupFlags 手工扫描 --name / --type（支持 --name=x 形态），余下的是位置
// 参数。不用 flag.FlagSet：它停在第一个非 flag 参数处，而组 ID 出现在 flag 前后都合法。
// 未知 flag 报错。
func parsePolicyGroupFlags(args []string) (name, typ string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var value string
		var hasValue, inline bool
		if eq := strings.Index(arg, "="); eq >= 0 && strings.HasPrefix(arg, "--") {
			value, hasValue, inline = arg[eq+1:], true, true
			arg = arg[:eq]
		} else if i+1 < len(args) {
			value, hasValue = args[i+1], true
		}
		switch arg {
		case "--name":
			if !hasValue {
				return "", "", nil, errors.New("--name requires a value")
			}
			name = value
			if !inline {
				i++
			}
		case "--type":
			if !hasValue {
				return "", "", nil, errors.New("--type requires a value")
			}
			typ = value
			if !inline {
				i++
			}
		default:
			if strings.HasPrefix(arg, "--") {
				return "", "", nil, fmt.Errorf("unknown flag %s", arg)
			}
			rest = append(rest, arg)
		}
	}
	return name, typ, rest, nil
}

// parsePolicyAttachArgs 扫描可重复的 --group 与位置参数（资产名 + 组 ID 列表）。
func parsePolicyAttachArgs(args []string) (groupNames, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--group=") {
			groupNames = append(groupNames, strings.TrimPrefix(arg, "--group="))
			continue
		}
		if arg == "--group" {
			if i+1 >= len(args) {
				return nil, nil, errors.New("--group requires a value")
			}
			groupNames = append(groupNames, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return nil, nil, fmt.Errorf("unknown flag %s", arg)
		}
		rest = append(rest, arg)
	}
	return groupNames, rest, nil
}

// --- 组 ID 形态与可写性（服务层不变式的 CLI 侧判定） ---

// ensureUserPolicyGroupID 校验组 ID 是数字的用户组并返回其数据库 ID；builtin / ext
// 形态只读，拒绝理由是 CLI 本地化消息并给出 copy 出路（服务层错误串是硬编码中文，
// 不直通输出）。写入类子命令（allow/deny/rm）在取组之前先过这道门。
func ensureUserPolicyGroupID(ctx context.Context, id string) (int64, error) {
	if policy_group_entity.IsBuiltinID(id) || policy_group_entity.IsExtensionID(id) {
		word := policy.PolicyMsg(ctx, "builtin", "内置")
		if policy_group_entity.IsExtensionID(id) {
			word = policy.PolicyMsg(ctx, "extension", "扩展")
		}
		return 0, errors.New(policy.PolicyFmt(ctx,
			"the %s policy group %s is read-only: builtin and extension groups cannot be edited or deleted. Copy it and change the copy: opsctl policy group copy %s --name <new-name>",
			"%s权限组 %s 是只读的：内置组与扩展组不能修改、不能删除。要改就先复制，在副本上改：opsctl policy group copy %s --name <新名>",
			word, id, id))
	}
	dbID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, errors.New(policy.PolicyFmt(ctx,
			"%q is not a policy group ID: expected builtin:<name>, ext:<name> or a numeric user group ID",
			"%q 不是权限组 ID：应为 builtin:<名称>、ext:<名称> 或数字的用户组 ID", id))
	}
	return dbID, nil
}

// getPolicyGroupItemLocalized 取组并把可预见的失败换成 CLI 本地化消息；其余服务层
// 错误按兜底直通（%v 原文）。
func getPolicyGroupItemLocalized(ctx context.Context, id string) (*policy_group_entity.PolicyGroupItem, error) {
	item, err := policy_group_svc.PolicyGroup().Get(ctx, id)
	if err == nil {
		return item, nil
	}
	if policy_group_entity.IsBuiltinID(id) || policy_group_entity.IsExtensionID(id) {
		return nil, errors.New(policy.PolicyFmt(ctx,
			"policy group %s not found (extension groups are only visible while the desktop app is running)",
			"权限组 %s 不存在（扩展组只在桌面端运行时可见）", id))
	}
	if _, perr := strconv.ParseInt(id, 10, 64); perr != nil {
		return nil, errors.New(policy.PolicyFmt(ctx,
			"%q is not a policy group ID: expected builtin:<name>, ext:<name> or a numeric user group ID",
			"%q 不是权限组 ID：应为 builtin:<名称>、ext:<名称> 或数字的用户组 ID", id))
	}
	return nil, fmt.Errorf("%w", err)
}

// knownPolicyGroupKind 从内置与扩展注册表派生合法 kind（Validate 的 isBuiltinKind 未
// 导出；扩展 kind 在 opsctl 进程内为空，与运行时可见性一致）。
func knownPolicyGroupKind(kind string) bool {
	for _, pg := range policy_group_entity.BuiltinGroups() {
		if pg.PolicyType == kind {
			return true
		}
	}
	for _, pg := range policy_group_entity.ExtensionGroups() {
		if pg.PolicyType == kind {
			return true
		}
	}
	return false
}

func policyGroupCanon(ctx context.Context, item *policy_group_entity.PolicyGroupItem) (string, error) {
	canon, ok := permission.CanonicalForPolicyKind(item.PolicyType)
	if !ok {
		return "", errors.New(policy.PolicyFmt(ctx,
			"policy group %s has type %q, which has no rule shape opsctl can work with",
			"权限组 %s 的类型 %q 没有 opsctl 可用的规则形状", item.ID, item.PolicyType))
	}
	return canon, nil
}

// policyGroupItemEntity 把 Item 还原成实体（数字 ID 复原；builtin/ext 保持 0——
// holder 只读写 Policy JSON 与 PolicyType）。
func policyGroupItemEntity(item *policy_group_entity.PolicyGroupItem) *policy_group_entity.PolicyGroup {
	pg := &policy_group_entity.PolicyGroup{
		Name:        item.Name,
		Description: item.Description,
		PolicyType:  item.PolicyType,
		Policy:      item.Policy,
		Createtime:  item.Createtime,
		Updatetime:  item.Updatetime,
	}
	if id, err := strconv.ParseInt(item.ID, 10, 64); err == nil {
		pg.ID = id
	}
	return pg
}

// --- 组自身规则的视图与条目（show 编号 / rm 定位 / 遮蔽检测共用） ---

// policyGroupRuleView 构造该组自身规则的单层视图（条目全部来自 RuleSourcePolicyGroup）。
func policyGroupRuleView(item *policy_group_entity.PolicyGroupItem, canon string) (*permission.TypeRuleView, error) {
	allow, deny, err := permission.HolderOwnTypeRules(permission.NewPolicyGroupHolder(policyGroupItemEntity(item)), canon)
	if err != nil {
		return nil, err
	}
	view := &permission.TypeRuleView{}
	for _, r := range allow {
		view.Allow = append(view.Allow, permission.SourcedRule{Rule: r,
			Kind: permission.RuleSourcePolicyGroup, PolicyGroupID: item.ID, PolicyGroupName: item.Name})
	}
	for _, r := range deny {
		view.Deny = append(view.Deny, permission.SourcedRule{Rule: r,
			Kind: permission.RuleSourcePolicyGroup, PolicyGroupID: item.ID, PolicyGroupName: item.Name})
	}
	return view, nil
}

// policyGroupRuleEntries 产出 show 条目：allow 先、deny 后连续编号——与资产视图同一
// 约定，rm 按编号定位同一条。
func policyGroupRuleEntries(item *policy_group_entity.PolicyGroupItem, canon string) ([]policyEntry, error) {
	view, err := policyGroupRuleView(item, canon)
	if err != nil {
		return nil, err
	}
	var entries []policyEntry
	next := 1
	for _, r := range view.Allow {
		e := policyEntry{id: strconv.Itoa(next), side: permission.RuleAllow, src: r, own: true}
		e.shadow = permission.ShadowingDeny(view, canon, r.Rule)
		entries = append(entries, e)
		next++
	}
	for _, r := range view.Deny {
		entries = append(entries, policyEntry{id: strconv.Itoa(next), side: permission.RuleDeny, src: r, own: true})
		next++
	}
	return entries, nil
}

// --- list / show（只读，免 TTY） ---

func cmdPolicyGroupList(ctx context.Context, args []string) int {
	_, typ, rest, err := parsePolicyGroupFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "Error: policy group list takes no positional arguments\n")
		return 1
	}
	if typ != "" && !knownPolicyGroupKind(typ) {
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx, "unknown policy type %q for --type", "--type 的策略类型 %q 未知", typ))
		return 1
	}
	items, err := policy_group_svc.PolicyGroup().List(ctx, typ)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "%s\n", policy.PolicyMsg(ctx, "policy groups:", "权限组：")) //nolint:errcheck // 终端呈现尽力而为
	for _, item := range items {
		line := fmt.Sprintf("  %-32s %-8s %s", item.ID, item.PolicyType, item.Name)
		if item.Description != "" {
			line += " - " + item.Description
		}
		fmt.Fprintf(os.Stdout, "%s\n", line) //nolint:errcheck // 终端呈现尽力而为
	}
	return 0
}

func cmdPolicyGroupShow(ctx context.Context, args []string) int {
	_, _, rest, err := parsePolicyGroupFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "Error: policy group show takes exactly one group id\n")
		return 1
	}
	item, err := getPolicyGroupItemLocalized(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	canon, err := policyGroupCanon(ctx, item)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	entries, err := policyGroupRuleEntries(item, canon)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	title := policy.PolicyFmt(ctx, "policy group %s (%s, type %s)", "权限组 %s（%s，类型 %s）", item.Name, item.ID, item.PolicyType)
	if item.Description != "" {
		title += " - " + item.Description
	}
	renderPolicyEntries(ctx, title, entries)
	if policy_group_entity.IsBuiltinID(item.ID) || policy_group_entity.IsExtensionID(item.ID) {
		fmt.Fprintf(os.Stdout, "\n%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
			"read-only group: builtin/extension groups cannot be edited or deleted; copy it to make changes: opsctl policy group copy %s --name <new-name>",
			"只读组：内置/扩展组不能修改、不能删除；要改就先复制：opsctl policy group copy %s --name <新名>", item.ID))
	}
	return 0
}

// --- create / copy ---

func cmdPolicyGroupCreate(ctx context.Context, args []string) int {
	name, typ, rest, err := parsePolicyGroupFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "Error: policy group create takes no positional arguments\n")
		return 1
	}
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, "policy group create")
	}
	if name == "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx,
			"--name is required: opsctl policy group create --name <name> --type <policy-type>",
			"必须传 --name：opsctl policy group create --name <名称> --type <策略类型>"))
		return 1
	}
	if typ == "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx,
			"--type is required: opsctl policy group create --name <name> --type <policy-type>",
			"必须传 --type：opsctl policy group create --name <名称> --type <策略类型>"))
		return 1
	}
	if !knownPolicyGroupKind(typ) {
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx, "unknown policy type %q for --type", "--type 的策略类型 %q 未知", typ))
		return 1
	}

	log := logger.Ctx(ctx)
	in, out := policyConfirmStreams()
	fmt.Fprintf(out, "%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
		"create policy group %s (type %s)?", "创建权限组 %s（类型 %s）？", name, typ))
	if !askRuleConfirm(in, out, policy.PolicyMsg(ctx, "create this group? [y/N]", "创建这个组？[y/N]")) {
		log.Info("opsctl policy group create declined at confirmation", zap.String("name", name))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}

	log.Info("opsctl policy group create started", zap.String("name", name), zap.String("type", typ))
	pg := &policy_group_entity.PolicyGroup{Name: name, PolicyType: typ}
	if err := policy_group_svc.PolicyGroup().Create(ctx, pg); err != nil {
		log.Error("opsctl policy group create failed", zap.String("name", name), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	auditJSON, err := policyGroupAuditArgsJSON(map[string]any{"group_id": pg.ID, "name": name, "type": typ})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := writePolicyGroupAudit(ctx, "created", auditJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	log.Info("opsctl policy group create completed", zap.Int64("groupID", pg.ID), zap.String("name", name))
	fmt.Fprintf(os.Stdout, "%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
		"created policy group %s (ID %d, type %s)", "已创建权限组 %s（ID %d，类型 %s）", name, pg.ID, typ))
	return 0
}

func cmdPolicyGroupCopy(ctx context.Context, args []string) int {
	name, _, rest, err := parsePolicyGroupFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "Error: policy group copy takes exactly one source group id and --name\n")
		return 1
	}
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, "policy group copy")
	}
	if name == "" {
		// 显式传名：不依赖服务层 Copy 的中文默认副本名。
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx,
			"copy needs an explicit --name (the new group's name): opsctl policy group copy %s --name <new-name>",
			"copy 必须显式传 --name（新组的名字）：opsctl policy group copy %s --name <新名>", rest[0]))
		return 1
	}
	item, err := getPolicyGroupItemLocalized(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	log := logger.Ctx(ctx)
	in, out := policyConfirmStreams()
	fmt.Fprintf(out, "%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
		"copy policy group %s (%s, type %s) to a new group named %s?",
		"把权限组 %s（%s，类型 %s）复制成一个名为 %s 的新组？", item.Name, item.ID, item.PolicyType, name))
	if !askRuleConfirm(in, out, policy.PolicyMsg(ctx, "copy this group? [y/N]", "复制这个组？[y/N]")) {
		log.Info("opsctl policy group copy declined at confirmation", zap.String("source", item.ID), zap.String("name", name))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}

	log.Info("opsctl policy group copy started", zap.String("source", item.ID), zap.String("name", name))
	created, err := policy_group_svc.PolicyGroup().Copy(ctx, item.ID, name)
	if err != nil {
		log.Error("opsctl policy group copy failed", zap.String("source", item.ID), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	auditJSON, err := policyGroupAuditArgsJSON(map[string]any{
		"source_id": item.ID, "source_name": item.Name, "new_id": created.ID, "name": name, "type": created.PolicyType,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := writePolicyGroupAudit(ctx, "copied", auditJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	log.Info("opsctl policy group copy completed", zap.Int64("newID", created.ID), zap.String("source", item.ID))
	fmt.Fprintf(os.Stdout, "%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
		"created policy group %s (ID %d, type %s) from %s",
		"已创建权限组 %s（ID %d，类型 %s），来源 %s", name, created.ID, created.PolicyType, item.ID))
	return 0
}

// --- allow / deny（写用户组自身策略，与 policy allow 同一落点机制） ---

func cmdPolicyGroupWrite(ctx context.Context, side permission.RuleSide, args []string) int {
	name := "policy group allow"
	if side == permission.RuleDeny {
		name = "policy group deny"
	}
	before, rawPatterns, err := splitPolicyArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	_, _, rest, err := parsePolicyGroupFlags(before)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "Error: %s takes exactly one group id before '--'\n", name)
		return 1
	}
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, name)
	}
	dbID, err := ensureUserPolicyGroupID(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	item, err := getPolicyGroupItemLocalized(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	canon, err := policyGroupCanon(ctx, item)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	normalized := make([]string, 0, len(rawPatterns))
	for _, p := range rawPatterns {
		norms := permission.NormalizeGrantPatterns(canon, p, permission.GrantOriginUser)
		if len(norms) == 0 {
			fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx,
				"pattern %q normalizes to nothing on type %s; nothing would be written",
				"pattern %q 在类型 %s 上归一化为空；不会写入任何规则", p, canon))
			return 1
		}
		normalized = append(normalized, norms...)
	}

	log := logger.Ctx(ctx)
	log.Info("opsctl policy group rule write started",
		zap.String("side", ruleSideName(side)), zap.Int64("groupID", dbID), zap.Int("patterns", len(normalized)))

	landed, shadow, err := policy_rule_svc.PolicyRule().PlanPolicyGroupRules(ctx, dbID, canon, side, normalized)
	if err != nil {
		log.Error("opsctl policy group rule write failed", zap.Int64("groupID", dbID), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 决策 19：被组内生效 deny 遮蔽的 allow 自始无效，写入前拒绝并给出出路。
	if shadow != nil {
		log.Warn("opsctl policy group rule write blocked by shadowing deny",
			zap.Int64("groupID", dbID), zap.String("deny", shadow.Rule))
		fmt.Fprintf(os.Stderr, "Error: %v\n", policyGroupShadowRefusal(ctx, item, shadow))
		return 1
	}

	label := policy.PolicyFmt(ctx, "policy group %s (%s)", "权限组 %s（%s）", item.Name, item.ID)
	if !confirmLandedRules(ctx, ruleSideName(side), []landedEcho{{label: label, canonical: canon, landed: landed}}) {
		log.Info("opsctl policy group rule write declined at confirmation", zap.Int64("groupID", dbID))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}

	rules := make([]string, 0, len(landed))
	for _, l := range landed {
		rules = append(rules, l.Rule)
	}
	// 审计参数在持久化之前序列化：marshal 失败时不落任何改动。
	auditJSON, err := policyGroupAuditArgsJSON(map[string]any{
		"group_id": dbID, "group": item.Name, "type": item.PolicyType,
		"side": ruleSideName(side), "patterns": normalized, "rules": rules,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := policy_rule_svc.PolicyRule().AppendPolicyGroupRules(ctx, dbID, canon, side, normalized); err != nil {
		log.Error("opsctl policy group rule write failed", zap.Int64("groupID", dbID), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := writePolicyGroupAudit(ctx, ruleSideName(side), auditJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	log.Info("opsctl policy group rule write completed",
		zap.String("side", ruleSideName(side)), zap.Int64("groupID", dbID))
	return 0
}

// policyGroupShadowRefusal 构造组内遮蔽拒绝：点名 deny 原文与来源，出路是组内撤条
// （show 查编号 → rm 撤编号），命令原文恒定 ASCII。
func policyGroupShadowRefusal(ctx context.Context, item *policy_group_entity.PolicyGroupItem, sh *permission.SourcedRule) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
		"refusing to write: this allow rule would never take effect inside this group, it is shadowed by a deny",
		"拒绝写入：这条 allow 规则在这个组里永远不会生效，它被一条 deny 遮蔽"))
	fmt.Fprintf(&sb, "%s %s\n", policy.PolicyMsg(ctx, "deny rule:", "deny 规则："), sh.Rule)
	fmt.Fprintf(&sb, "%s %s\n", policy.PolicyMsg(ctx, "source:", "来源："), sourcedRuleOrigin(ctx, *sh))
	fmt.Fprintf(&sb, "%s\n", policy.PolicyMsg(ctx,
		"to fix: remove the deny entry from this group (find the entry id via show):",
		"出路：先撤掉组里的这条 deny（编号用 show 查）："))
	fmt.Fprintf(&sb, "  opsctl policy group show %s\n", item.ID)
	fmt.Fprintf(&sb, "  opsctl policy group rm %s <entry-id>\n", item.ID)
	return errors.New(strings.TrimRight(sb.String(), "\n"))
}

// --- rm（撤单条编号 / 删整个用户组） ---

func cmdPolicyGroupRm(ctx context.Context, args []string) int {
	_, _, rest, err := parsePolicyGroupFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(rest) == 0 || len(rest) > 2 {
		fmt.Fprintf(os.Stderr, "Error: policy group rm takes one group id and optionally one entry id (see 'opsctl policy group show')\n")
		return 1
	}
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, "policy group rm")
	}
	dbID, err := ensureUserPolicyGroupID(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	item, err := getPolicyGroupItemLocalized(ctx, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	canon, err := policyGroupCanon(ctx, item)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	log := logger.Ctx(ctx)
	in, out := policyConfirmStreams()

	if len(rest) == 1 {
		fmt.Fprintf(out, "%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
			"delete policy group %s (%s, type %s) and all its rules?",
			"删除权限组 %s（%s，类型 %s）及其全部规则？", item.Name, item.ID, item.PolicyType))
		if !askRuleConfirm(in, out, policy.PolicyMsg(ctx, "delete this group? [y/N]", "删除这个组？[y/N]")) {
			log.Info("opsctl policy group rm declined at confirmation", zap.Int64("groupID", dbID))
			fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
			return 1
		}
		log.Info("opsctl policy group rm started", zap.Int64("groupID", dbID))
		auditJSON, err := policyGroupAuditArgsJSON(map[string]any{
			"group_id": dbID, "group": item.Name, "type": item.PolicyType,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		if err := policy_group_svc.PolicyGroup().Delete(ctx, rest[0]); err != nil {
			log.Error("opsctl policy group rm failed", zap.Int64("groupID", dbID), zap.Error(err))
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		if err := writePolicyGroupAudit(ctx, "deleted", auditJSON); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		log.Info("opsctl policy group rm completed", zap.Int64("groupID", dbID))
		return 0
	}

	// 撤单条：编号来自 show（allow 先 deny 后）。
	entries, err := policyGroupRuleEntries(item, canon)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	entry, ok := findPolicyEntry(entries, rest[1])
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx,
			"no entry %s in this group; run 'opsctl policy group show %s' to list entry ids",
			"组里没有编号 %s 的条目；用 'opsctl policy group show %s' 查编号", rest[1], item.ID))
		return 1
	}
	fmt.Fprintf(out, "%s\n", policy.PolicyMsg(ctx, "rule to be removed:", "将要删除的规则：")) //nolint:errcheck // 终端呈现尽力而为
	fmt.Fprintf(out, "  %s %s\n", ruleSideName(entry.side), entry.src.Rule)            //nolint:errcheck // 终端呈现尽力而为
	if !askRuleConfirm(in, out, policy.PolicyMsg(ctx, "remove this rule? [y/N]", "删除这条规则？[y/N]")) {
		log.Info("opsctl policy group rm declined at confirmation", zap.Int64("groupID", dbID), zap.String("entry", rest[1]))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}
	log.Info("opsctl policy group rm started", zap.Int64("groupID", dbID), zap.String("entry", rest[1]))
	auditJSON, err := policyGroupAuditArgsJSON(map[string]any{
		"group_id": dbID, "group": item.Name, "type": item.PolicyType,
		"side": ruleSideName(entry.side), "rule": entry.src.Rule,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := policy_rule_svc.PolicyRule().RemovePolicyGroupRule(ctx, dbID, canon, entry.side, entry.src.Rule); err != nil {
		log.Error("opsctl policy group rm failed", zap.Int64("groupID", dbID), zap.String("entry", rest[1]), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := writePolicyGroupAudit(ctx, "removed", auditJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	log.Info("opsctl policy group rm completed", zap.Int64("groupID", dbID), zap.String("rule", entry.src.Rule))
	return 0
}

// renderPolicyGroupRefError 把服务层 attach/detach 前置校验失败渲染成本地化消息
// （决策 21/22）：业务判定在服务层，文案归属 CLI；未知错误原样透传。
func renderPolicyGroupRefError(ctx context.Context, err error, target policy_rule_svc.Target) error {
	var refErr *policy_rule_svc.PolicyGroupRefError
	if errors.As(err, &refErr) {
		switch refErr.Reason {
		case policy_rule_svc.GroupRefReasonTypeMismatch:
			return errors.New(policy.PolicyFmt(ctx,
				"refusing to attach: policy group %s (%s) has type %s but asset %s has type %s - its rules would never take effect",
				"拒绝挂载：权限组 %s（%s）的类型是 %s，而资产 %s 的类型是 %s——规则永远不会生效",
				refErr.Ref.Name, refErr.Ref.ID, refErr.Ref.PolicyType, refErr.Target.Asset.Name, refErr.Target.Asset.Type))
		case policy_rule_svc.GroupRefReasonNoShape:
			return errors.New(policy.PolicyFmt(ctx,
				"policy group %s has type %q, which has no rule shape opsctl can attach",
				"权限组 %s 的类型 %q 没有 opsctl 可挂载的规则形状", refErr.Ref.ID, refErr.Ref.PolicyType))
		default:
			return errors.New(policy.PolicyFmt(ctx,
				"asset %s has type %s, which has no policy column a group can be attached to",
				"资产 %s 的类型 %s 没有可挂权限组的策略列", refErr.Target.Asset.Name, refErr.Target.Asset.Type))
		}
	}
	var stateErr *policy_rule_svc.GroupRefStateError
	if errors.As(err, &stateErr) {
		if stateErr.Attach {
			return errors.New(policy.PolicyFmt(ctx,
				"policy group %s (%s) is already attached to %s",
				"权限组 %s（%s）已经挂在 %s 上", stateErr.Ref.Name, stateErr.Ref.ID, policyTargetLabel(target)))
		}
		return errors.New(policy.PolicyFmt(ctx,
			"policy group %s (%s) is not attached to %s",
			"权限组 %s（%s）没有挂在 %s 上", stateErr.Ref.Name, stateErr.Ref.ID, policyTargetLabel(target)))
	}
	return err
}

// --- attach / detach：holder 策略列的 Groups 引用 ---

func cmdPolicyAttachDetach(ctx context.Context, attach bool, args []string) int {
	name := "policy attach"
	if !attach {
		name = "policy detach"
	}
	groupNames, rest, err := parsePolicyAttachArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if !isInteractive(stdinIsTerminal(), stderrIsTerminal()) {
		return refusePolicyWrite(ctx, name)
	}

	var target policy_rule_svc.Target
	switch {
	case len(groupNames) == 1 && len(rest) >= 1:
		gid, _, err := resolveGroup(ctx, groupNames[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		group, err := policy_rule_svc.PolicyRule().FindGroup(ctx, gid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: group not found: ID %d\n", gid)
			return 1
		}
		target = policy_rule_svc.Target{Group: group}
	case len(groupNames) == 0 && len(rest) >= 2:
		asset, err := resolveAsset(ctx, rest[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		target = policy_rule_svc.Target{Asset: asset}
		rest = rest[1:]
	default:
		fmt.Fprintf(os.Stderr, "Error: %s takes one target (<asset> or --group <group>) and one or more group ids\n", name)
		return 1
	}

	items := make([]*policy_group_entity.PolicyGroupItem, 0, len(rest))
	for _, gid := range rest {
		item, err := getPolicyGroupItemLocalized(ctx, gid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		items = append(items, item)
	}

	serviceTarget := target
	refs := make([]policy_rule_svc.GroupRef, 0, len(items))
	for _, item := range items {
		refs = append(refs, policy_rule_svc.GroupRef{ID: item.ID, Name: item.Name, PolicyType: item.PolicyType})
	}
	if err := policy_rule_svc.PolicyRule().ValidateGroupRefs(ctx, serviceTarget, refs, attach); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", renderPolicyGroupRefError(ctx, err, serviceTarget))
		return 1
	}

	log := logger.Ctx(ctx)
	in, out := policyConfirmStreams()
	echoHeader := policy.PolicyMsg(ctx, "policy groups to attach:", "将要挂载的权限组：")
	if !attach {
		echoHeader = policy.PolicyMsg(ctx, "policy groups to detach:", "将要摘除的权限组：")
	}
	fmt.Fprintf(out, "%s\n", echoHeader) //nolint:errcheck // 终端呈现尽力而为
	for _, item := range items {
		fmt.Fprintf(out, "  %s (%s, type %s) -> %s\n", item.Name, item.ID, item.PolicyType, policyTargetLabel(target)) //nolint:errcheck // 终端呈现尽力而为
	}
	prompt := policy.PolicyMsg(ctx, "attach these groups? [y/N]", "挂载这些组？[y/N]")
	if !attach {
		prompt = policy.PolicyMsg(ctx, "detach these groups? [y/N]", "摘除这些组？[y/N]")
	}
	if !askRuleConfirm(in, out, prompt) {
		log.Info("opsctl policy attach declined at confirmation",
			zap.Bool("attach", attach), zap.String("target", policyTargetLabel(target)))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}

	verb := "attach"
	if !attach {
		verb = "detach"
	}
	log.Info("opsctl policy attach started", zap.String("verb", verb), zap.String("target", policyTargetLabel(target)), zap.Int("groups", len(items)))

	ids := make([]string, 0, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		names = append(names, item.Name)
	}
	auditArgs := map[string]any{"groups": ids, "group_names": names}
	if target.Asset != nil {
		auditArgs["asset_id"] = target.Asset.ID
		auditArgs["asset"] = target.Asset.Name
	} else {
		auditArgs["group_id"] = target.Group.ID
		auditArgs["group"] = target.Group.Name
	}
	auditJSON, err := policyGroupAuditArgsJSON(auditArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := policy_rule_svc.PolicyRule().UpdateGroupRefs(ctx, serviceTarget, refs, attach); err != nil {
		log.Error("opsctl policy attach failed", zap.String("verb", verb), zap.String("target", policyTargetLabel(target)), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", renderPolicyGroupRefError(ctx, err, serviceTarget))
		return 1
	}
	if err := writePolicyGroupAudit(ctx, verb, auditJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	log.Info("opsctl policy attach completed", zap.String("verb", verb), zap.String("target", policyTargetLabel(target)))
	return 0
}

// --- 审计 ---

// policyGroupAuditArgsJSON 序列化组操作审计参数。多数调用方在持久化之前序列化
// （marshal 失败时不落任何改动）；create / copy 因审计参数含持久化后生成的 ID，
// 在持久化之后序列化，其参数只含可序列化基础值，marshal 实际不会失败。
func policyGroupAuditArgsJSON(args map[string]any) (string, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal policy group audit args: %w", err)
	}
	return string(b), nil
}

// writePolicyGroupAudit 为一次成功的组操作记一行审计（与 policy_rule 同一工具名，
// result 区分动词）。argsJSON 由调用方序列化（create / copy 在持久化后、其余在改动前）。
func writePolicyGroupAudit(ctx context.Context, result, argsJSON string) error {
	// 成功落库的组操作记 allow 决策（审计写入器按 Deny 决策把 success 置 0）；
	// 操作语义（deny 侧 / 删除 / 摘除）由 result 与 args 表达。
	decision := &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
	writeOpsctlAudit(ctx, "policy_rule", argsJSON, result, nil, decision)
	return nil
}
