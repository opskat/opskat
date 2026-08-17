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
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
	"github.com/opskat/opskat/internal/service/policy_group_svc"
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

// policyGroupKindCanon 把组 kind 映射到注册了同形状规则落点的代表性 canonical 资产
// 类型——permission 的落点 API 按资产类型查表，CLI 经这张表复用它读写用户组。
var policyGroupKindCanon = map[string]string{
	policyent.PolicyKindCommand: asset_entity.AssetTypeSSH,
	policyent.PolicyKindQuery:   asset_entity.AssetTypeDatabase,
	policyent.PolicyKindRedis:   asset_entity.AssetTypeRedis,
	policyent.PolicyKindMongo:   asset_entity.AssetTypeMongoDB,
	policyent.PolicyKindKafka:   asset_entity.AssetTypeKafka,
	policyent.PolicyKindK8s:     asset_entity.AssetTypeK8s,
	policyent.PolicyKindEtcd:    asset_entity.AssetTypeEtcd,
	policyent.PolicyKindOSS:     asset_entity.AssetTypeOSS,
}

func policyGroupCanon(ctx context.Context, item *policy_group_entity.PolicyGroupItem) (string, error) {
	canon, ok := policyGroupKindCanon[item.PolicyType]
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

// --- PolicyGroup 的 Holder 适配（不改 internal 的前提下复用 T5 落点） ---

// policyGroupHolder 把 PolicyGroup 适配成 policyent.Holder（含写面），让 permission
// 的落点函数（AppendTypeRules / RemoveTypeRule / HolderOwnTypeRules）直接读写用户组
// 的策略 JSON。Policy 为空串按零值形状处理（刚 create 的组）。Set 侧以 PolicyType
// 守门：形状表配错列时宁可报错也不静默落错列。
type policyGroupHolder struct {
	pg *policy_group_entity.PolicyGroup
}

func policyGroupPolicyOf[T any](pg *policy_group_entity.PolicyGroup) (*T, error) {
	p := new(T)
	if pg.Policy == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(pg.Policy), p); err != nil {
		return nil, fmt.Errorf("unmarshal policy group %q policy: %w", pg.Name, err)
	}
	return p, nil
}

func (h *policyGroupHolder) setPolicy(kind string, p any) error {
	if h.pg.PolicyType != kind {
		return fmt.Errorf("policy group %q has type %s; refusing to write its %s column", h.pg.Name, h.pg.PolicyType, kind)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	h.pg.Policy = string(data)
	return nil
}

func (h *policyGroupHolder) GetCommandPolicy() (*policyent.CommandPolicy, error) {
	return policyGroupPolicyOf[policyent.CommandPolicy](h.pg)
}

func (h *policyGroupHolder) GetQueryPolicy() (*policyent.QueryPolicy, error) {
	return policyGroupPolicyOf[policyent.QueryPolicy](h.pg)
}

func (h *policyGroupHolder) GetRedisPolicy() (*policyent.RedisPolicy, error) {
	return policyGroupPolicyOf[policyent.RedisPolicy](h.pg)
}

func (h *policyGroupHolder) GetMongoPolicy() (*policyent.MongoPolicy, error) {
	return policyGroupPolicyOf[policyent.MongoPolicy](h.pg)
}

func (h *policyGroupHolder) GetKafkaPolicy() (*policyent.KafkaPolicy, error) {
	return policyGroupPolicyOf[policyent.KafkaPolicy](h.pg)
}

func (h *policyGroupHolder) GetK8sPolicy() (*policyent.K8sPolicy, error) {
	return policyGroupPolicyOf[policyent.K8sPolicy](h.pg)
}

func (h *policyGroupHolder) GetEtcdPolicy() (*policyent.EtcdPolicy, error) {
	return policyGroupPolicyOf[policyent.EtcdPolicy](h.pg)
}

func (h *policyGroupHolder) GetOSSPolicy() (*policyent.OSSPolicy, error) {
	return policyGroupPolicyOf[policyent.OSSPolicy](h.pg)
}

func (h *policyGroupHolder) SetCommandPolicy(p *policyent.CommandPolicy) error {
	return h.setPolicy(policyent.PolicyKindCommand, p)
}

func (h *policyGroupHolder) SetQueryPolicy(p *policyent.QueryPolicy) error {
	return h.setPolicy(policyent.PolicyKindQuery, p)
}

func (h *policyGroupHolder) SetRedisPolicy(p *policyent.RedisPolicy) error {
	return h.setPolicy(policyent.PolicyKindRedis, p)
}

func (h *policyGroupHolder) SetMongoPolicy(p *policyent.MongoPolicy) error {
	return h.setPolicy(policyent.PolicyKindMongo, p)
}

func (h *policyGroupHolder) SetKafkaPolicy(p *policyent.KafkaPolicy) error {
	return h.setPolicy(policyent.PolicyKindKafka, p)
}

func (h *policyGroupHolder) SetK8sPolicy(p *policyent.K8sPolicy) error {
	return h.setPolicy(policyent.PolicyKindK8s, p)
}

func (h *policyGroupHolder) SetEtcdPolicy(p *policyent.EtcdPolicy) error {
	return h.setPolicy(policyent.PolicyKindEtcd, p)
}

func (h *policyGroupHolder) SetOSSPolicy(p *policyent.OSSPolicy) error {
	return h.setPolicy(policyent.PolicyKindOSS, p)
}

// --- 组自身规则的视图与条目（show 编号 / rm 定位 / 遮蔽检测共用） ---

// policyGroupRuleView 构造该组自身规则的单层视图（条目全部来自 RuleSourcePolicyGroup）。
func policyGroupRuleView(item *policy_group_entity.PolicyGroupItem, canon string) (*permission.TypeRuleView, error) {
	allow, deny, err := permission.HolderOwnTypeRules(&policyGroupHolder{pg: policyGroupItemEntity(item)}, canon)
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
	writePolicyGroupAudit(ctx, "created", map[string]any{"group_id": pg.ID, "name": name, "type": typ}, false)
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
	writePolicyGroupAudit(ctx, "copied", map[string]any{
		"source_id": item.ID, "source_name": item.Name, "new_id": created.ID, "name": name, "type": created.PolicyType,
	}, false)
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

	holder := &policyGroupHolder{pg: policyGroupItemEntity(item)}
	landed, err := permission.AppendTypeRules(holder, canon, side, normalized)
	if err != nil {
		log.Error("opsctl policy group rule write failed", zap.Int64("groupID", dbID), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 决策 19：被组内生效 deny 遮蔽的 allow 自始无效，写入前拒绝并给出出路。
	if side == permission.RuleAllow {
		view, err := policyGroupRuleView(item, canon)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		for _, l := range landed {
			if sh := permission.ShadowingDeny(view, canon, l.Rule); sh != nil {
				log.Warn("opsctl policy group rule write blocked by shadowing deny",
					zap.Int64("groupID", dbID), zap.String("deny", sh.Rule))
				fmt.Fprintf(os.Stderr, "Error: %v\n", policyGroupShadowRefusal(ctx, item, sh))
				return 1
			}
		}
	}

	label := policy.PolicyFmt(ctx, "policy group %s (%s)", "权限组 %s（%s）", item.Name, item.ID)
	if !confirmLandedRules(ctx, ruleSideName(side), []landedEcho{{label: label, canonical: canon, landed: landed}}) {
		log.Info("opsctl policy group rule write declined at confirmation", zap.Int64("groupID", dbID))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}

	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		fresh, err := policy_group_repo.PolicyGroup().Find(txCtx, dbID)
		if err != nil {
			return err
		}
		if _, err := permission.AppendTypeRules(&policyGroupHolder{pg: fresh}, canon, side, normalized); err != nil {
			return err
		}
		return policy_group_svc.PolicyGroup().Update(txCtx, fresh)
	}); err != nil {
		log.Error("opsctl policy group rule write failed", zap.Int64("groupID", dbID), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	rules := make([]string, 0, len(landed))
	for _, l := range landed {
		rules = append(rules, l.Rule)
	}
	writePolicyGroupAudit(ctx, ruleSideName(side), map[string]any{
		"group_id": dbID, "group": item.Name, "type": item.PolicyType,
		"side": ruleSideName(side), "patterns": normalized, "rules": rules,
	}, side == permission.RuleDeny)
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
		if err := policy_group_svc.PolicyGroup().Delete(ctx, rest[0]); err != nil {
			log.Error("opsctl policy group rm failed", zap.Int64("groupID", dbID), zap.Error(err))
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		writePolicyGroupAudit(ctx, "deleted", map[string]any{
			"group_id": dbID, "group": item.Name, "type": item.PolicyType,
		}, true)
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
	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		fresh, err := policy_group_repo.PolicyGroup().Find(txCtx, dbID)
		if err != nil {
			return err
		}
		if err := permission.RemoveTypeRule(&policyGroupHolder{pg: fresh}, canon, entry.side, entry.src.Rule); err != nil {
			return err
		}
		return policy_group_svc.PolicyGroup().Update(txCtx, fresh)
	}); err != nil {
		log.Error("opsctl policy group rm failed", zap.Int64("groupID", dbID), zap.String("entry", rest[1]), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	writePolicyGroupAudit(ctx, "removed", map[string]any{
		"group_id": dbID, "group": item.Name, "type": item.PolicyType,
		"side": ruleSideName(entry.side), "rule": entry.src.Rule,
	}, true)
	log.Info("opsctl policy group rm completed", zap.Int64("groupID", dbID), zap.String("rule", entry.src.Rule))
	return 0
}

// --- attach / detach：holder 策略列的 Groups 引用 ---

// cliPolicyWriter 是 CLI 侧的 holder 写面（镜像 permission 未导出的 policyRWHolder；
// Asset 与 Group 都实现）。
type cliPolicyWriter interface {
	policyent.Holder
	SetCommandPolicy(*policyent.CommandPolicy) error
	SetQueryPolicy(*policyent.QueryPolicy) error
	SetRedisPolicy(*policyent.RedisPolicy) error
	SetMongoPolicy(*policyent.MongoPolicy) error
	SetKafkaPolicy(*policyent.KafkaPolicy) error
	SetK8sPolicy(*policyent.K8sPolicy) error
	SetEtcdPolicy(*policyent.EtcdPolicy) error
	SetOSSPolicy(*policyent.OSSPolicy) error
}

var (
	_ cliPolicyWriter = (*asset_entity.Asset)(nil)
	_ cliPolicyWriter = (*group_entity.Group)(nil)
)

// policyGroupRefColumn 是一个策略列的 Groups 引用读写对（attach/detach 的落点）。
type policyGroupRefColumn struct {
	refs  func(cliPolicyWriter) ([]string, error)
	apply func(w cliPolicyWriter, mutate func(refs *[]string) error) error
}

func policyGroupRefColumnFor[T any](
	get func(cliPolicyWriter) (*T, error),
	set func(cliPolicyWriter, *T) error,
	refs func(*T) *[]string,
) policyGroupRefColumn {
	return policyGroupRefColumn{
		refs: func(w cliPolicyWriter) ([]string, error) {
			p, err := get(w)
			if err != nil {
				return nil, err
			}
			return append([]string(nil), *refs(p)...), nil
		},
		apply: func(w cliPolicyWriter, mutate func(refs *[]string) error) error {
			p, err := get(w)
			if err != nil {
				return err
			}
			if err := mutate(refs(p)); err != nil {
				return err
			}
			return set(w, p)
		},
	}
}

// policyGroupRefColumns 按 policy kind 选择 Groups 落点列（policy/policy.go:7 的
// Groups 字段，与 allow/deny 同一列）。
var policyGroupRefColumns = map[string]policyGroupRefColumn{
	policyent.PolicyKindCommand: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.CommandPolicy, error) { return w.GetCommandPolicy() },
		func(w cliPolicyWriter, p *policyent.CommandPolicy) error { return w.SetCommandPolicy(p) },
		func(p *policyent.CommandPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindQuery: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.QueryPolicy, error) { return w.GetQueryPolicy() },
		func(w cliPolicyWriter, p *policyent.QueryPolicy) error { return w.SetQueryPolicy(p) },
		func(p *policyent.QueryPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindRedis: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.RedisPolicy, error) { return w.GetRedisPolicy() },
		func(w cliPolicyWriter, p *policyent.RedisPolicy) error { return w.SetRedisPolicy(p) },
		func(p *policyent.RedisPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindMongo: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.MongoPolicy, error) { return w.GetMongoPolicy() },
		func(w cliPolicyWriter, p *policyent.MongoPolicy) error { return w.SetMongoPolicy(p) },
		func(p *policyent.MongoPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindKafka: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.KafkaPolicy, error) { return w.GetKafkaPolicy() },
		func(w cliPolicyWriter, p *policyent.KafkaPolicy) error { return w.SetKafkaPolicy(p) },
		func(p *policyent.KafkaPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindK8s: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.K8sPolicy, error) { return w.GetK8sPolicy() },
		func(w cliPolicyWriter, p *policyent.K8sPolicy) error { return w.SetK8sPolicy(p) },
		func(p *policyent.K8sPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindEtcd: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.EtcdPolicy, error) { return w.GetEtcdPolicy() },
		func(w cliPolicyWriter, p *policyent.EtcdPolicy) error { return w.SetEtcdPolicy(p) },
		func(p *policyent.EtcdPolicy) *[]string { return &p.Groups },
	),
	policyent.PolicyKindOSS: policyGroupRefColumnFor(
		func(w cliPolicyWriter) (*policyent.OSSPolicy, error) { return w.GetOSSPolicy() },
		func(w cliPolicyWriter, p *policyent.OSSPolicy) error { return w.SetOSSPolicy(p) },
		func(p *policyent.OSSPolicy) *[]string { return &p.Groups },
	),
}

// attachColumnKind 决定挂载写的 Groups 列（policy kind）。资产目标：列由资产类型的
// asset-kind 注册表决定——k8s 落 K8sPolicy 列但可挂的组按 command 表解析（type_registry.go
// 注册语义的镜像）——且组的 PolicyType 必须匹配，否则写入前拒绝（规则挂错列等于永不
// 生效）。组目标：列由所挂组自己的 PolicyType 决定，任何 kind 都可生效。
func attachColumnKind(ctx context.Context, t *policyWriteTarget, item *policy_group_entity.PolicyGroupItem) (string, error) {
	if t.asset != nil {
		column, registered := policyent.AssetKindOf(t.asset.Type)
		if !registered || column == "" {
			return "", errors.New(policy.PolicyFmt(ctx,
				"asset %s has type %s, which has no policy column a group can be attached to",
				"资产 %s 的类型 %s 没有可挂权限组的策略列", t.asset.Name, t.asset.Type))
		}
		accepted := column
		if column == policyent.PolicyKindK8s {
			accepted = policyent.PolicyKindCommand
		}
		if item.PolicyType != accepted {
			return "", errors.New(policy.PolicyFmt(ctx,
				"refusing to attach: policy group %s (%s) has type %s but asset %s has type %s - its rules would never take effect",
				"拒绝挂载：权限组 %s（%s）的类型是 %s，而资产 %s 的类型是 %s——规则永远不会生效",
				item.Name, item.ID, item.PolicyType, t.asset.Name, t.asset.Type))
		}
		return column, nil
	}
	if _, ok := policyGroupRefColumns[item.PolicyType]; !ok {
		return "", errors.New(policy.PolicyFmt(ctx,
			"policy group %s has type %q, which has no rule shape opsctl can attach",
			"权限组 %s 的类型 %q 没有 opsctl 可挂载的规则形状", item.ID, item.PolicyType))
	}
	return item.PolicyType, nil
}

func targetPolicyWriter(t *policyWriteTarget) cliPolicyWriter {
	if t.asset != nil {
		return t.asset
	}
	return t.group
}

func freshPolicyTarget(ctx context.Context, t *policyWriteTarget) (*policyWriteTarget, error) {
	if t.asset != nil {
		fresh, err := asset_repo.Asset().Find(ctx, t.asset.ID)
		if err != nil {
			return nil, err
		}
		return &policyWriteTarget{asset: fresh}, nil
	}
	fresh, err := group_repo.Group().Find(ctx, t.group.ID)
	if err != nil {
		return nil, err
	}
	return &policyWriteTarget{group: fresh}, nil
}

func containsStr(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

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

	var target policyWriteTarget
	switch {
	case len(groupNames) == 1 && len(rest) >= 1:
		gid, _, err := resolveGroup(ctx, groupNames[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		group, err := group_repo.Group().Find(ctx, gid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: group not found: ID %d\n", gid)
			return 1
		}
		target = policyWriteTarget{group: group}
	case len(groupNames) == 0 && len(rest) >= 2:
		asset, err := resolveAsset(ctx, rest[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		target = policyWriteTarget{asset: asset}
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

	// 列选择与冲突预检都在写入之前：类型不匹配、已挂（attach）、未挂（detach）直接失败。
	type attachOp struct {
		item   *policy_group_entity.PolicyGroupItem
		column policyGroupRefColumn
	}
	w := targetPolicyWriter(&target)
	ops := make([]attachOp, 0, len(items))
	for _, item := range items {
		columnKind, err := attachColumnKind(ctx, &target, item)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		column := policyGroupRefColumns[columnKind]
		current, err := column.refs(w)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		if attach && containsStr(current, item.ID) {
			fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx,
				"policy group %s (%s) is already attached to %s",
				"权限组 %s（%s）已经挂在 %s 上", item.Name, item.ID, target.label()))
			return 1
		}
		if !attach && !containsStr(current, item.ID) {
			fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyFmt(ctx,
				"policy group %s (%s) is not attached to %s",
				"权限组 %s（%s）没有挂在 %s 上", item.Name, item.ID, target.label()))
			return 1
		}
		ops = append(ops, attachOp{item: item, column: column})
	}

	log := logger.Ctx(ctx)
	in, out := policyConfirmStreams()
	echoHeader := policy.PolicyMsg(ctx, "policy groups to attach:", "将要挂载的权限组：")
	if !attach {
		echoHeader = policy.PolicyMsg(ctx, "policy groups to detach:", "将要摘除的权限组：")
	}
	fmt.Fprintf(out, "%s\n", echoHeader) //nolint:errcheck // 终端呈现尽力而为
	for _, op := range ops {
		fmt.Fprintf(out, "  %s (%s, type %s) -> %s\n", op.item.Name, op.item.ID, op.item.PolicyType, target.label()) //nolint:errcheck // 终端呈现尽力而为
	}
	prompt := policy.PolicyMsg(ctx, "attach these groups? [y/N]", "挂载这些组？[y/N]")
	if !attach {
		prompt = policy.PolicyMsg(ctx, "detach these groups? [y/N]", "摘除这些组？[y/N]")
	}
	if !askRuleConfirm(in, out, prompt) {
		log.Info("opsctl policy attach declined at confirmation",
			zap.Bool("attach", attach), zap.String("target", target.label()))
		fmt.Fprintf(os.Stderr, "Error: %s\n", policy.PolicyMsg(ctx, "declined: nothing written", "已拒绝：未写入任何改动"))
		return 1
	}

	verb := "attach"
	if !attach {
		verb = "detach"
	}
	log.Info("opsctl policy attach started", zap.String("verb", verb), zap.String("target", target.label()), zap.Int("groups", len(ops)))
	if err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		fresh, err := freshPolicyTarget(txCtx, &target)
		if err != nil {
			return err
		}
		fw := targetPolicyWriter(fresh)
		for _, op := range ops {
			id := op.item.ID
			if err := op.column.apply(fw, func(refs *[]string) error {
				if attach {
					if !containsStr(*refs, id) {
						*refs = append(*refs, id)
					}
					return nil
				}
				kept := make([]string, 0, len(*refs))
				for _, r := range *refs {
					if r == id {
						continue
					}
					kept = append(kept, r)
				}
				*refs = kept
				return nil
			}); err != nil {
				return err
			}
		}
		if fresh.asset != nil {
			return asset_repo.Asset().Update(txCtx, fresh.asset)
		}
		return group_repo.Group().Update(txCtx, fresh.group)
	}); err != nil {
		log.Error("opsctl policy attach failed", zap.String("verb", verb), zap.String("target", target.label()), zap.Error(err))
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	ids := make([]string, 0, len(ops))
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		ids = append(ids, op.item.ID)
		names = append(names, op.item.Name)
	}
	auditArgs := map[string]any{"groups": ids, "group_names": names}
	if target.asset != nil {
		auditArgs["asset_id"] = target.asset.ID
		auditArgs["asset"] = target.asset.Name
	} else {
		auditArgs["group_id"] = target.group.ID
		auditArgs["group"] = target.group.Name
	}
	writePolicyGroupAudit(ctx, verb, auditArgs, !attach)
	log.Info("opsctl policy attach completed", zap.String("verb", verb), zap.String("target", target.label()))
	return 0
}

// --- 审计 ---

// writePolicyGroupAudit 为一次成功的组操作记一行审计（与 policy_rule 同一工具名，
// result 区分动词）。
func writePolicyGroupAudit(ctx context.Context, result string, args map[string]any, deny bool) {
	argsJSON, _ := json.Marshal(args)
	decision := &aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow}
	if deny {
		decision = &aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}
	}
	writeOpsctlAudit(ctx, "policy_rule", string(argsJSON), result, nil, decision)
}
