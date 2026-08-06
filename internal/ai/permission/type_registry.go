package permission

import (
	"context"
	"fmt"
	"sort"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

type permissionCheckFunc func(context.Context, int64, string) aictx.CheckResult

// permissionTypeHandler is the single source of truth for permission dispatch,
// opsctl aliases, approval item types, and grant normalization behavior.
//
// grantPatterns 把一条审批输入拆成可独立匹配的 grant pattern（见 NormalizeGrantPatterns）。
// nil 表示"整串存一条"，是绝大多数类型的形态；shell 类按 AST 子命令拆，OSS 按 DSL 派生
// 策略串，cp 把系统给出的路径主体转义成只匹配它自己的规则。这里是一个注册的函数而不是一个
// shellLike 布尔开关，因为要选的本来就是这个函数——第三种归一化方式出现时，布尔开关只能
// 变成 dispatcher 里的类型分支（设计 D8）。
//
// origin 说的是这条输入是谁写的（见 GrantOrigin）：字符串本身分不出"系统推导"与
// "用户手写"，而只有前者该被收窄，所以来源必须由调用方声明着传进来。
type permissionTypeHandler struct {
	canonical     string
	approvalType  string
	grantPatterns GrantPatternsFunc
	check         permissionCheckFunc
}

var permissionTypes = make(map[string]*permissionTypeHandler)

func registerPermissionType(canonical, approvalType string, grantPatterns GrantPatternsFunc, check permissionCheckFunc, aliases ...string) {
	if canonical == "" || approvalType == "" || check == nil {
		panic("permission: invalid type registration")
	}
	handler := &permissionTypeHandler{
		canonical:     canonical,
		approvalType:  approvalType,
		grantPatterns: grantPatterns,
		check:         check,
	}
	for _, name := range append([]string{canonical}, aliases...) {
		if _, exists := permissionTypes[name]; exists {
			panic(fmt.Sprintf("permission: duplicate type registration %q", name))
		}
		permissionTypes[name] = handler
	}
}

func permissionTypeFor(name string) (*permissionTypeHandler, bool) {
	handler, ok := permissionTypes[name]
	return handler, ok
}

// ApprovalTypeFor 返回该资产类型在审批面板上的类型标签（前端 TypeBadge 按它取图标）。
// 未注册类型回落到原样返回——审批项宁可显示一个陌生标签，也不该静默变成 "exec"。
func ApprovalTypeFor(assetType string) string {
	if handler, ok := permissionTypeFor(assetType); ok {
		return handler.approvalType
	}
	return assetType
}

// SupportsGrantApproval reports whether an approval type is registered with a
// permission checker and therefore has a defined pattern-matching contract. CRUD and
// other one-shot operations are deliberately absent from the registry and must not
// expose allowAll merely because they share the single-approval dialog shape.
func SupportsGrantApproval(approvalType string) bool {
	_, ok := permissionTypeFor(approvalType)
	return ok
}

func init() {
	registerPermissionType(asset_entity.AssetTypeSSH, "exec", shellGrantPatterns, checkCommandPolicyPermission, "exec")
	registerPermissionType(asset_entity.AssetTypeSerial, "serial", nil, checkCommandPolicyPermission)
	registerPermissionType(asset_entity.AssetTypeDatabase, "sql", nil, checkDatabasePermission, "sql")
	registerPermissionType(asset_entity.AssetTypeRedis, "redis", nil, checkRedisPermission)
	registerPermissionType(asset_entity.AssetTypeEtcd, "etcd", nil, checkEtcdPermission)
	registerPermissionType(asset_entity.AssetTypeMongoDB, "mongo", nil, checkMongoDBPermission, "mongo")
	registerPermissionType(asset_entity.AssetTypeKafka, "kafka", nil, checkKafkaPermission)
	registerPermissionType(asset_entity.AssetTypeK8s, "k8s", shellGrantPatterns, checkK8sPermission)
	registerPermissionType(asset_entity.AssetTypeOSS, "oss", ossGrantPatterns, checkOSSPermission)
	// cp 不是资产类型而是操作面：任何能开 SFTP 的资产上的文件传输都归它，主体是远端路径
	// 而非命令，所以不按 shell 子命令拆；cpGrantPatterns 做的是另一件事——把系统给出的
	// 主体转义成"只匹配它自己"的规则（决策 D21），因为路径里的 `* ? [` 可以是字面文件名。
	registerPermissionType(GrantToolCp, "cp", cpGrantPatterns, checkFileTransferPermission)
	registerPermissionType(GrantToolCpRead, "cp", cpGrantPatterns, checkFileTransferReadPermission)
	registerPermissionType(GrantToolCpWrite, "cp", cpGrantPatterns, checkFileTransferWritePermission)
}

// --- 执行器注册表 ---
//
// 注册方向是自下而上推送：helper 等持有协议代码的包导入本包，
// 因此本包只声明类型与注册入口，实现由它们在 init() 中调 RegisterExecutor 推上来。

// ExecFunc 按资产真实类型执行一条命令。scope 是"不属于命令本身的连接级目标"
// （database 用库名、redis 用 db 序号），其余类型忽略。
type ExecFunc func(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error)

// CanonicalizeFunc 把模型给的原始命令规范化为"真正会被执行、也应被策略匹配"的形式。
// 仅当某类型执行前会改写命令时才需要注册（k8s 注入 --context/--namespace；etcd 走
// ParseCommand+FormatCommand 的 round trip，规范化大小写/复合命令拼写/flag 顺序）。
type CanonicalizeFunc func(asset *asset_entity.Asset, command string) (string, error)

// PolicyStringsFunc 把一条命令展开为它实际请求的权限串（`<action> <resource>`）。
//
// 只有"一条命令请求多项权限"的类型需要它：OSS 的 `object copy` 同时读源、写目的，
// `object move` 还要删源，只按其中一条判定就等于放行"把受限对象复制到可读位置再读"
// 的绕过路径（设计 D7）。其余类型的命令与权限一一对应，不注册。
//
// 注册方向与 CanonicalizeFunc / PrecheckFunc 相同，理由却更硬：DSL 解析器在持有协议
// 代码的包里（internal/ai/helper），而那个包**已经 import 本包**
// （transfer_ssh.go 取 GrantToolCp），本包再反向 import 就是 import cycle。
// 因此本包只声明入口，由 internal/ai/execimpl 在 init() 里连同执行器一起推上来。
type PolicyStringsFunc func(command string) ([]string, error)

var policyStringFuncs = make(map[string]PolicyStringsFunc)

// RegisterPolicyStrings 注册某资产类型的策略串派生函数。
// 重复/空注册 panic，与 RegisterExecutor 同一原则：注册冲突是启动期的编程错误。
//
// 没有注册时，依赖它的权限检查按"解析失败"处理并退回 NeedConfirm——fail-closed，
// 且漏接线不会被静默吞掉：该类型的每条命令都要弹一次审批，而不是悄悄放行。
func RegisterPolicyStrings(canonical string, fn PolicyStringsFunc) {
	if canonical == "" || fn == nil {
		panic("permission: invalid policy strings registration")
	}
	if _, exists := policyStringFuncs[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate policy strings registration %q", canonical))
	}
	policyStringFuncs[canonical] = fn
}

// UnregisterPolicyStringsForTest 移除一个已注册的派生函数，仅供测试清理使用——
// 与 UnregisterExecutorForTest 同一理由：`-count>1` 会重跑同一个测试函数，
// 而生产注册路径的 panic-on-duplicate 是有意保留的。
func UnregisterPolicyStringsForTest(canonical string) {
	delete(policyStringFuncs, canonical)
}

func policyStringsFor(canonical string) (PolicyStringsFunc, bool) {
	fn, ok := policyStringFuncs[canonical]
	return fn, ok
}

// PrecheckFunc 校验某类型执行前的前置条件——与 CanonicalizeFunc 一样，存在的唯一理由是
// 把一个必然会失败的检查挪到权限检查（可能弹审批对话框）之前，避免用户先被打断、
// 批准之后命令才因为这个检查而失败。仅当某类型有这类检查时才需要注册（目前只有
// serial：会话不存在）。与 CanonicalizeFunc 不同的是它不改写命令，只返回错误。
type PrecheckFunc func(ctx context.Context, asset *asset_entity.Asset) error

type execEntry struct {
	exec         ExecFunc
	help         string
	canonicalize CanonicalizeFunc
	precheck     PrecheckFunc
}

var execEntries = make(map[string]*execEntry)

// RegisterExecutor 注册某资产类型的执行器与用法文档。canonicalize 是可选的第四个参数——
// 只有执行前会改写命令的类型（目前是 k8s、etcd）才需要传；不传的类型按原样校验与执行。
// 重复注册 panic——与 registerPermissionType 一致，注册冲突是启动期的编程错误，不该静默覆盖。
//
// help == "" 同样 panic，与 RegisterHelpDoc 的校验对齐：这条守卫是本包唯一能兜住
// "调用方拿到一个空字符串却还是调用了注册函数"的地方——execimpl/register.go 的每个
// exec 类型都从 skills.Get 取 help，若那次调用漏检了 ok（曾经发生过：8 处全写成
// `sshHelp, _ := skills.Get(...)`），SKILL.md 缺失就会静默注册成一个内容为空的执行器：
// HelpFor 仍然返回 ("", true)，help_coverage_test.go 的 TestEveryAssetTypeHasHelpDoc
// 只检查 ok、不检查内容，因此测试保持全绿，而模型实际拿到的用法文档是空的。
func RegisterExecutor(canonical string, exec ExecFunc, help string, canonicalize ...CanonicalizeFunc) {
	if canonical == "" || exec == nil || help == "" {
		panic("permission: invalid executor registration")
	}
	if _, exists := execEntries[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate executor registration %q", canonical))
	}
	var canon CanonicalizeFunc
	if len(canonicalize) > 0 {
		canon = canonicalize[0]
	}
	execEntries[canonical] = &execEntry{exec: exec, help: help, canonicalize: canon}
}

// RegisterHelpDoc 只注册用法文档，不注册执行器——给没有命令面、但可以被 put_asset
// 创建/更新的类型用（rdp / vnc / oss / local）。它们的 SKILL.md 只写配置字段。
//
// 与 RegisterExecutor 共用同一张 execEntries 表，因此 HelpFor 天然可用；
// 而 ExecutorFor / RegisteredExecTypes 会跳过 exec == nil 的条目——
// exec 对这些类型必须报"尚不支持"，不能查到一个 nil 函数再 panic。
func RegisterHelpDoc(canonical, help string) {
	if canonical == "" || help == "" {
		panic("permission: invalid help-doc registration")
	}
	if _, exists := execEntries[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate help-doc registration %q", canonical))
	}
	execEntries[canonical] = &execEntry{help: help}
}

// ExecutorFor 返回该资产类型的执行器。doc-only 条目（exec == nil）报 (nil, false)——
// 调用方不能查到一个 nil 函数再去调用它。
func ExecutorFor(assetType string) (ExecFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok || entry.exec == nil {
		return nil, false
	}
	return entry.exec, true
}

// HelpFor 返回该资产类型的用法文档。
func HelpFor(assetType string) (string, bool) {
	entry, ok := execEntries[assetType]
	if !ok {
		return "", false
	}
	return entry.help, true
}

// CanonicalizeFor 返回该资产类型注册的命令规范化钩子（如有）。没有注册规范化钩子
// 的类型（多数类型）返回 (nil, false)——调用方应按原样校验与执行。
func CanonicalizeFor(assetType string) (CanonicalizeFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok || entry.canonicalize == nil {
		return nil, false
	}
	return entry.canonicalize, true
}

// RegisterPrecheck 为一个已注册执行器的资产类型追加可选的 PrecheckFunc。必须在
// RegisterExecutor 之后调用——precheck 挂在已存在的 execEntry 上，就像 CanonicalizeFunc
// 一样。重复注册 panic，与 RegisterExecutor 的重复注册检查同一原则：注册冲突是启动期
// 编程错误，不该被静默覆盖。
func RegisterPrecheck(canonical string, precheck PrecheckFunc) {
	entry, ok := execEntries[canonical]
	if !ok {
		panic(fmt.Sprintf("permission: RegisterPrecheck on unregistered executor %q", canonical))
	}
	if entry.precheck != nil {
		panic(fmt.Sprintf("permission: duplicate precheck registration %q", canonical))
	}
	entry.precheck = precheck
}

// PrecheckFor 返回该资产类型注册的前置条件检查（如有）。没有注册 precheck 的类型
// （绝大多数类型）返回 (nil, false)——调用方不应把 false 当成"允许"以外的任何含义，
// 只是"没有额外检查要跑"。
func PrecheckFor(assetType string) (PrecheckFunc, bool) {
	entry, ok := execEntries[assetType]
	if !ok || entry.precheck == nil {
		return nil, false
	}
	return entry.precheck, true
}

// UnregisterExecutorForTest 移除一个已注册的执行器。仅供包外测试使用：测试想验证
// "check 用规范化命令、exec 用原始命令"这类跨类型行为时，需要注册一个临时的假类型
// 并在结束后清理（同一个测试 binary 内 `-count>1` 会重复执行同一个测试函数，
// RegisterExecutor 撞见重复注册会 panic）。RegisterExecutor 本身的 panic-on-duplicate
// 是有意的——那是给生产 init() 用的，编程期冲突不该被这个函数悄悄放过。
func UnregisterExecutorForTest(canonical string) {
	delete(execEntries, canonical)
}

// RegisteredExecTypes 返回**能执行命令**的资产类型，已排序。doc-only 条目不在其中——
// 这份清单会进模型看到的 exec 工具描述，把只有配置文档的类型列进去等于承诺一个做不到的能力。
func RegisteredExecTypes() []string {
	types := make([]string, 0, len(execEntries))
	for name, entry := range execEntries {
		if entry.exec == nil {
			continue
		}
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}

// RegisteredHelpTypes 返回有用法文档的资产类型（有执行器的 + doc-only 的），已排序。
// put_asset 的错误信息与 prompt 的类型清单用它。
func RegisteredHelpTypes() []string {
	types := make([]string, 0, len(execEntries))
	for name := range execEntries {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
