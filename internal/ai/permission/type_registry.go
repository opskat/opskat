package permission

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
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

// registryMu 保护本文件的两张注册表（permissionTypes / execEntries）。内置类型在 init()
// 里一次写完，扩展提供的类型则在用户启用/禁用扩展时增删，与 AI 会话、opsctl socket 上
// 正在跑的读取并发。
var registryMu sync.RWMutex

var permissionTypes = make(map[string]*permissionTypeHandler)

func registerPermissionType(canonical, approvalType string, grantPatterns GrantPatternsFunc, check permissionCheckFunc, aliases ...string) {
	if err := addPermissionType(canonical, approvalType, grantPatterns, check, aliases...); err != nil {
		panic(err.Error())
	}
}

func addPermissionType(canonical, approvalType string, grantPatterns GrantPatternsFunc, check permissionCheckFunc, aliases ...string) error {
	if canonical == "" || approvalType == "" || check == nil {
		return fmt.Errorf("permission: invalid type registration")
	}
	handler := &permissionTypeHandler{
		canonical:     canonical,
		approvalType:  approvalType,
		grantPatterns: grantPatterns,
		check:         check,
	}
	names := append([]string{canonical}, aliases...)
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, name := range names {
		if _, exists := permissionTypes[name]; exists {
			return fmt.Errorf("permission: duplicate type registration %q", name)
		}
	}
	for _, name := range names {
		permissionTypes[name] = handler
	}
	return nil
}

// PolicyCheckFunc 是一个资产类型自带的策略判定：给定资产与（已规范化的）命令，
// 返回 Allow / Deny / NeedConfirm。
//
// 它存在的理由与 CanonicalizeFunc / PrecheckFunc / PolicyStringsFunc 相同——判定逻辑
// 住在持有协议代码的包里，本包只声明入口——但方向更硬：扩展提供的资产类型的判定要调
// WASM guest 的 check_policy，而那份代码在 pkg/extension 里，加载时才存在。
//
// 注册进来之后，该类型走的是**同一条** CheckForAsset：NeedConfirm 会经 HandleConfirm
// 弹审批框，"全部允许"照常落 grant，下一次同类命令由 grant 匹配直接放行。绕开这条路
// 自己调 ConfirmFunc 的写法（曾经的 ext_exec）恰恰是把 grant 整条丢掉的原因。
type PolicyCheckFunc func(ctx context.Context, assetID int64, command string) aictx.CheckResult

// RegisterPolicyCheck 注册一个运行期可再移除的资产类型的策略检查。审批面标签取类型名
// 本身（与 ApprovalTypeFor 对未注册类型的回落一致），grant pattern 整串存一条。
// 冲突返回错误而不是 panic：冲突来自用户装了两个声明同一类型的扩展。
func RegisterPolicyCheck(canonical string, check PolicyCheckFunc) error {
	if check == nil {
		return fmt.Errorf("permission: invalid policy check registration %q", canonical)
	}
	return addPermissionType(canonical, canonical, nil, permissionCheckFunc(check))
}

// UnregisterPolicyCheck 移除一个由 RegisterPolicyCheck 注册的类型。
func UnregisterPolicyCheck(canonical string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(permissionTypes, canonical)
}

func permissionTypeFor(name string) (*permissionTypeHandler, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
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
	registerPermissionType(asset_entity.AssetTypeDatabase, "sql", nil, checkDatabasePermission, "sql", "db")
	registerPermissionType(asset_entity.AssetTypeRedis, "redis", nil, checkRedisPermission)
	registerPermissionType(asset_entity.AssetTypeEtcd, "etcd", nil, checkEtcdPermission)
	registerPermissionType(asset_entity.AssetTypeMongoDB, "mongo", nil, checkMongoDBPermission, "mongo")
	registerPermissionType(asset_entity.AssetTypeKafka, "kafka", nil, checkKafkaPermission)
	registerPermissionType(asset_entity.AssetTypeK8s, "k8s", shellGrantPatterns, checkK8sPermission, "kubernetes", "kube")
	registerPermissionType(asset_entity.AssetTypeOSS, "oss", ossGrantPatterns, checkOSSPermission)
	// cp 不是资产类型而是操作面：任何能开 SFTP 的资产上的文件传输都归它，主体是远端路径
	// 而非命令，所以不按 shell 子命令拆；cpGrantPatterns 做的是另一件事——把系统给出的
	// 主体转义成"只匹配它自己"的规则（决策 D21），因为路径里的 `* ? [` 可以是字面文件名。
	registerPermissionType(GrantToolCp, "cp", cpGrantPatterns, checkFileTransferPermission)
	registerPermissionType(GrantToolCpRead, "cp", cpGrantPatterns, checkFileTransferReadPermission)
	registerPermissionType(GrantToolCpWrite, "cp", cpGrantPatterns, checkFileTransferWritePermission)

	// 永久规则落点与上面的 grantPatterns 并列注册（spec 决策 11、15）：一个类型一次
	// 注册、同时覆盖 allow 与 deny 两侧、按 holder 取 Get/SetXxxPolicy 对。
	commandShape := registerRuleShape(policyKindCommand, shapeSides[policyent.CommandPolicy]{
		get: func(h policyent.Holder) (*policyent.CommandPolicy, error) { return h.GetCommandPolicy() },
		set: func(h policyRWHolder, p *policyent.CommandPolicy) error { return h.SetCommandPolicy(p) },
		sides: func(p *policyent.CommandPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.CommandPolicy { return &policyent.CommandPolicy{} },
	})
	queryShape := registerRuleShape(policyKindQuery, shapeSides[policyent.QueryPolicy]{
		get: func(h policyent.Holder) (*policyent.QueryPolicy, error) { return h.GetQueryPolicy() },
		set: func(h policyRWHolder, p *policyent.QueryPolicy) error { return h.SetQueryPolicy(p) },
		sides: func(p *policyent.QueryPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowTypes, &p.DenyTypes, &p.Groups
		},
		newOne: func() *policyent.QueryPolicy { return &policyent.QueryPolicy{} },
	})
	redisShape := registerRuleShape(policyKindRedis, shapeSides[policyent.RedisPolicy]{
		get: func(h policyent.Holder) (*policyent.RedisPolicy, error) { return h.GetRedisPolicy() },
		set: func(h policyRWHolder, p *policyent.RedisPolicy) error { return h.SetRedisPolicy(p) },
		sides: func(p *policyent.RedisPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.RedisPolicy { return &policyent.RedisPolicy{} },
	})
	mongoShape := registerRuleShape(policyKindMongo, shapeSides[policyent.MongoPolicy]{
		get: func(h policyent.Holder) (*policyent.MongoPolicy, error) { return h.GetMongoPolicy() },
		set: func(h policyRWHolder, p *policyent.MongoPolicy) error { return h.SetMongoPolicy(p) },
		sides: func(p *policyent.MongoPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowTypes, &p.DenyTypes, &p.Groups
		},
		newOne: func() *policyent.MongoPolicy { return &policyent.MongoPolicy{} },
	})
	kafkaShape := registerRuleShape(policyKindKafka, shapeSides[policyent.KafkaPolicy]{
		get: func(h policyent.Holder) (*policyent.KafkaPolicy, error) { return h.GetKafkaPolicy() },
		set: func(h policyRWHolder, p *policyent.KafkaPolicy) error { return h.SetKafkaPolicy(p) },
		sides: func(p *policyent.KafkaPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.KafkaPolicy { return &policyent.KafkaPolicy{} },
	})
	k8sShape := registerRuleShape(policyKindK8s, shapeSides[policyent.K8sPolicy]{
		get: func(h policyent.Holder) (*policyent.K8sPolicy, error) { return h.GetK8sPolicy() },
		set: func(h policyRWHolder, p *policyent.K8sPolicy) error { return h.SetK8sPolicy(p) },
		sides: func(p *policyent.K8sPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.K8sPolicy { return &policyent.K8sPolicy{} },
	})
	etcdShape := registerRuleShape(policyKindEtcd, shapeSides[policyent.EtcdPolicy]{
		get: func(h policyent.Holder) (*policyent.EtcdPolicy, error) { return h.GetEtcdPolicy() },
		set: func(h policyRWHolder, p *policyent.EtcdPolicy) error { return h.SetEtcdPolicy(p) },
		sides: func(p *policyent.EtcdPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.EtcdPolicy { return &policyent.EtcdPolicy{} },
	})
	ossShape := registerRuleShape(policyKindOSS, shapeSides[policyent.OSSPolicy]{
		get: func(h policyent.Holder) (*policyent.OSSPolicy, error) { return h.GetOSSPolicy() },
		set: func(h policyRWHolder, p *policyent.OSSPolicy) error { return h.SetOSSPolicy(p) },
		sides: func(p *policyent.OSSPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.OSSPolicy { return &policyent.OSSPolicy{} },
	})
	genericRuleLanding.shape = commandShape

	registerRuleSink(asset_entity.AssetTypeSSH, &ruleLanding{
		shape: commandShape, refPolicyType: policyKindCommand, land: identityLand, match: policy.MatchCommandRule})
	registerRuleSink(asset_entity.AssetTypeSerial, &ruleLanding{
		shape: commandShape, refPolicyType: policyKindCommand, land: identityLand, match: policy.MatchCommandRule})
	// k8s 的 K8sPolicy 是独立列，但引用组按 command 表解析（collectK8sPolicies 用
	// ResolveCommandGroups），并且先判组通用 CommandPolicy 层。
	registerRuleSink(asset_entity.AssetTypeK8s, &ruleLanding{
		shape: k8sShape, refPolicyType: policyKindCommand, land: identityLand,
		match: policy.MatchCommandRule, generic: policy.MatchCommandRule})
	registerRuleSink(asset_entity.AssetTypeDatabase, &ruleLanding{
		shape: queryShape, refPolicyType: policyKindQuery, land: queryLand,
		match: queryTypeMatch, generic: policy.MatchCommandRule})
	registerRuleSink(asset_entity.AssetTypeRedis, &ruleLanding{
		shape: redisShape, refPolicyType: policyKindRedis, land: identityLand,
		match: policy.MatchRedisRule, generic: policy.MatchRedisRule})
	registerRuleSink(asset_entity.AssetTypeEtcd, &ruleLanding{
		shape: etcdShape, refPolicyType: policyKindEtcd, land: identityLand,
		match: policy.MatchRedisRule, generic: policy.MatchRedisRule})
	registerRuleSink(asset_entity.AssetTypeMongoDB, &ruleLanding{
		shape: mongoShape, refPolicyType: policyKindMongo, land: mongoLand,
		match: policy.MatchMongoRule, generic: policy.MatchMongoRule})
	registerRuleSink(asset_entity.AssetTypeKafka, &ruleLanding{
		shape: kafkaShape, refPolicyType: policyKindKafka, land: identityLand,
		match: policy.MatchKafkaRule, generic: policy.MatchCommandRule})
	registerRuleSink(asset_entity.AssetTypeOSS, &ruleLanding{
		shape: ossShape, refPolicyType: policyKindOSS, land: identityLand,
		match: policy.MatchOSSRule, generic: policy.MatchOSSRule})
	// cp 面：规则落在 CommandPolicy 列、带方向前缀（matchCpPolicyRule 的规则语法）。
	// 无方向的 GrantToolCp（"cp"）不注册——落点与遮蔽都需要方向，绝不猜默认形状。
	registerRuleSink(GrantToolCpRead, &ruleLanding{
		shape: commandShape, refPolicyType: policyKindCommand, land: cpLand("cp:read:"), match: cpDenyShadows})
	registerRuleSink(GrantToolCpWrite, &ruleLanding{
		shape: commandShape, refPolicyType: policyKindCommand, land: cpLand("cp:write:"), match: cpDenyShadows})
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
// 与 UnregisterExecutor 同一理由：`-count>1` 会重跑同一个测试函数，
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
	if err := RegisterDynamicExecutor(canonical, exec, help, canonicalize...); err != nil {
		panic(err.Error())
	}
}

// RegisterDynamicExecutor 与 RegisterExecutor 写同一张表，只是把重复/非法注册报成错误
// 而不是 panic：扩展提供的资产类型是用户在运行期启用/禁用的，一次冲突不该让应用崩掉。
func RegisterDynamicExecutor(canonical string, exec ExecFunc, help string, canonicalize ...CanonicalizeFunc) error {
	if canonical == "" || exec == nil || help == "" {
		return fmt.Errorf("permission: invalid executor registration")
	}
	var canon CanonicalizeFunc
	if len(canonicalize) > 0 {
		canon = canonicalize[0]
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := execEntries[canonical]; exists {
		return fmt.Errorf("permission: duplicate executor registration %q", canonical)
	}
	execEntries[canonical] = &execEntry{exec: exec, help: help, canonicalize: canon}
	return nil
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
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := execEntries[canonical]; exists {
		panic(fmt.Sprintf("permission: duplicate help-doc registration %q", canonical))
	}
	execEntries[canonical] = &execEntry{help: help}
}

// ExecutorFor 返回该资产类型的执行器。doc-only 条目（exec == nil）报 (nil, false)——
// 调用方不能查到一个 nil 函数再去调用它。
func ExecutorFor(assetType string) (ExecFunc, bool) {
	entry, ok := execEntryFor(assetType)
	if !ok || entry.exec == nil {
		return nil, false
	}
	return entry.exec, true
}

func execEntryFor(assetType string) (*execEntry, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	entry, ok := execEntries[assetType]
	return entry, ok
}

// HelpFor 返回该资产类型的用法文档。
func HelpFor(assetType string) (string, bool) {
	entry, ok := execEntryFor(assetType)
	if !ok {
		return "", false
	}
	return entry.help, true
}

// CanonicalizeFor 返回该资产类型注册的命令规范化钩子（如有）。没有注册规范化钩子
// 的类型（多数类型）返回 (nil, false)——调用方应按原样校验与执行。
func CanonicalizeFor(assetType string) (CanonicalizeFunc, bool) {
	entry, ok := execEntryFor(assetType)
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
	registryMu.Lock()
	defer registryMu.Unlock()
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
	entry, ok := execEntryFor(assetType)
	if !ok || entry.precheck == nil {
		return nil, false
	}
	return entry.precheck, true
}

// UnregisterExecutor 移除一个已注册的执行器或用法文档。
//
// 生产调用方是扩展卸载/禁用（internal/extreg）：扩展提供的资产类型随扩展一起来去。
// 测试也用它清理临时注册的假类型——同一个测试 binary 内 `-count>1` 会重复执行同一个
// 测试函数，而 RegisterExecutor 的 panic-on-duplicate 是有意保留给生产 init() 的。
func UnregisterExecutor(canonical string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(execEntries, canonical)
}

// RegisteredExecTypes 返回**能执行命令**的资产类型，已排序。doc-only 条目不在其中——
// 这份清单会进模型看到的 exec 工具描述，把只有配置文档的类型列进去等于承诺一个做不到的能力。
func RegisteredExecTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
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
	registryMu.RLock()
	defer registryMu.RUnlock()
	types := make([]string, 0, len(execEntries))
	for name := range execEntries {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}
