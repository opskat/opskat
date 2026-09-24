// Package extreg 把一个已加载的扩展接进宿主的各张全局注册表：资产类型
// （internal/assettype）、执行器与策略检查（internal/ai/permission）、默认策略与权限组
// （internal/model/entity/policy*）。
//
// 它存在的理由是方向：这些注册表都在 internal/ 下，而扩展的运行期对象在 pkg/extension，
// 由 internal/service/extension_svc 的生命周期驱动。把接线集中在这里，Bridge 才能塌回
// 一张 name → *Extension 的表，而消费点（exec / help / 审批 / 前端表单）不再需要"内置一
// 条路、扩展一条路"。
//
// 注册是全有或全无：任何一步失败都会把这个扩展已经写进去的条目全部撤掉并返回错误，
// 调用方据此拒绝加载该扩展。半注册的扩展比不注册更糟——资产类型在表单里出现，exec 却
// 找不到执行器。
package extreg

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	aipolicy "github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/pkg/extension"
)

// helpLang 是渲染扩展用法文档时解析 i18n key 用的语言。文档在加载期渲染一次并注册进
// permission，而模型的对话语言是会话期才知道的；内置类型的 SKILL.md 同样只有英文。
const helpLang = "en"

// pluginCaller 是本包用到的 WASM 插件能力子集（*extension.Plugin 满足它）。
// 收窄到这几个方法而不是直接吃 *Plugin，是为了让策略/执行/配置校验这几条闭包可以在
// 没有 wazero 运行时的情况下被测试驱动——它们承载的是行为契约，不是 WASM 加载。
type pluginCaller interface {
	CallTool(ctx context.Context, toolName string, args json.RawMessage, asset *extension.AssetRef) (json.RawMessage, error)
	CheckPolicy(ctx context.Context, toolName string, args json.RawMessage) (action, resource string, err error)
	ValidateConfig(ctx context.Context, config json.RawMessage) ([]extension.ValidationError, error)
	TestConnectionCaller
}

// TestConnectionCaller is the one plugin capability a conntest tester needs:
// run an asset type's describe()-declared test-connection handler. It is its
// own (exported) interface, not folded silently into pluginCaller, because
// ConnTestRegistrar — implemented outside this package — needs a type it can
// name in its own signature; Go only requires the method sets to match.
type TestConnectionCaller interface {
	TestConnection(ctx context.Context, assetType string, adhoc *extension.AdHocAssetConfig) error
}

// ConnTestRegistrar builds the conntest.TestFunc a describe()-declared
// test-connection handler is registered under. The one implementation
// (internal/app/extension, wired once from main.go via SetConnTestRegistrar)
// needs extension_svc and credential_svc to merge the form's unchanged
// password fields with an edited asset's stored ones — internal/extreg
// cannot import either without an import cycle (extension_svc already calls
// extreg.Register), so the capability is injected instead of implemented here.
type ConnTestRegistrar interface {
	Build(extName string, manifest *extension.Manifest, assetType string, plugin TestConnectionCaller) conntest.TestFunc
}

var connTestRegistrar ConnTestRegistrar

// SetConnTestRegistrar wires the registrar main.go constructs at startup,
// before any extension loads. Called more than once, the last call wins —
// there is exactly one desktop process wiring this, same as every other
// setter-injected seam in main.go.
func SetConnTestRegistrar(r ConnTestRegistrar) { connTestRegistrar = r }

// loaded 是一个已加载扩展在本包内的最小画像。
type loaded struct {
	name     string
	manifest *extension.Manifest
	plugin   pluginCaller
}

var (
	mu         sync.Mutex
	registered = make(map[string][]string) // extension name → registered asset types
	// policyTypeOwner 记录每个策略面（manifest 的 policies.type）归哪个扩展。策略面是
	// 权限组 ID（ext:<policyType>:<name>）与永久规则（ext:<policyType>:<action>）共同的
	// 命名空间段，也是 CheckExtensionPolicy 筛权限组的键：两个扩展共用一个策略面，
	// 一方的组与规则就会被当成另一方的来判。
	policyTypeOwner = make(map[string]string) // policy type → extension name
)

// claimPolicyType 把 policyType 记到 extName 名下；已被别的扩展占用时拒绝。调用方持有 mu。
func claimPolicyType(extName, policyType string) error {
	if policy_group_entity.IsBuiltinPolicyType(policyType) {
		return fmt.Errorf("extension %q: policy type %q is a built-in policy type", extName, policyType)
	}
	if owner, taken := policyTypeOwner[policyType]; taken {
		return fmt.Errorf("extension %q: policy type %q is already owned by extension %q", extName, policyType, owner)
	}
	policyTypeOwner[policyType] = extName
	return nil
}

// Register 把 ext 声明的每个资产类型接进宿主注册表。
func Register(ext *extension.Extension) error {
	localized := ext.Manifest.Localized(func(key string) string { return ext.Translate(helpLang, key) })
	return register(
		loaded{name: ext.Name, manifest: ext.Manifest, plugin: ext.Plugin},
		helpDocument(ext.SkillMD, ext.Name, localized),
		skillDescription(ext),
	)
}

func register(l loaded, help, description string) error {
	m := l.manifest
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registered[l.name]; exists {
		return fmt.Errorf("extension %q is already registered", l.name)
	}
	if err := claimPolicyType(l.name, m.Policies.Type); err != nil {
		return err
	}

	var done []string
	rollback := func() {
		for _, t := range done {
			unregisterType(t)
		}
		policy_group_entity.UnregisterExtensionGroupsByExtension(l.name)
		delete(policyTypeOwner, m.Policies.Type)
	}

	for _, at := range m.AssetTypes {
		if err := registerType(l, at, help, description); err != nil {
			rollback()
			return err
		}
		done = append(done, at.Type)
	}

	if err := registerPolicyGroups(l.name, m); err != nil {
		rollback()
		return err
	}

	registered[l.name] = done
	logger.Default().Info("extension registered",
		zap.String("extension", l.name), zap.Strings("assetTypes", done))
	return nil
}

// Unregister 撤掉一个扩展写进宿主注册表的全部条目。未注册的名字是 no-op。
func Unregister(name string) {
	mu.Lock()
	defer mu.Unlock()
	types, ok := registered[name]
	if !ok {
		return
	}
	for _, t := range types {
		unregisterType(t)
	}
	policy_group_entity.UnregisterExtensionGroupsByExtension(name)
	for policyType, owner := range policyTypeOwner {
		if owner == name {
			delete(policyTypeOwner, policyType)
		}
	}
	delete(registered, name)
	logger.Default().Info("extension unregistered",
		zap.String("extension", name), zap.Strings("assetTypes", types))
}

func registerType(l loaded, at extension.AssetTypeDef, help, description string) error {
	m := l.manifest
	// 测试连接要跑 WASM，只有桌面进程接了 registrar；先查，免得登记到一半再回滚。
	if at.TestConnection && connTestRegistrar == nil {
		return fmt.Errorf("extension %q: asset type %q declares a test-connection handler but no registrar is wired", l.name, at.Type)
	}
	if err := assettype.RegisterExtensionType(extensionTypeSpec(l.name, m, at)); err != nil {
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	if err := permission.RegisterPolicyCheck(at.Type, policyCheck(l, at.Type), classifyForApproval(l)); err != nil {
		assettype.Unregister(at.Type)
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	if err := permission.RegisterDynamicExecutor(at.Type, execTool(l), help, canonicalize(l)); err != nil {
		assettype.Unregister(at.Type)
		permission.UnregisterPolicyCheck(at.Type)
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	// 技能清单与 help 文档分开登记，和内置类型完全一样（execimpl 也是先 skills.Get
	// 再 RegisterExecutor）：清单进 prompt，让模型知道这个类型存在、help 能问；
	// 正文只在模型真的调了 help 之后才下发。
	if err := skills.RegisterDynamic(at.Type, help, description); err != nil {
		assettype.Unregister(at.Type)
		permission.UnregisterPolicyCheck(at.Type)
		permission.UnregisterExecutor(at.Type)
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	// 永久规则落点：opsctl policy allow/deny/rm/show 走它，规则形状
	// `ext:<policyType>:<action>`，落在共用的 CommandPolicy 列上。
	if err := permission.RegisterExtensionRuleSink(at.Type, m.Policies.Type, m.Policies.Actions); err != nil {
		assettype.Unregister(at.Type)
		permission.UnregisterPolicyCheck(at.Type)
		permission.UnregisterExecutor(at.Type)
		skills.UnregisterDynamic(at.Type)
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	// 配置校验：guest 的 RegisterConfigValidator 挂到资产写入口（asset_svc），桌面表单、
	// put_asset、opsctl create 落库前都经它。仅描述注册（opsctl 进程）没有 plugin，不注册——
	// 那边只有 configSchema 的必填/未知字段校验。
	if err := asset_entity.RegisterConfigValidator(at.Type, validateConfig(l)); err != nil {
		assettype.Unregister(at.Type)
		permission.UnregisterPolicyCheck(at.Type)
		permission.UnregisterExecutor(at.Type)
		skills.UnregisterDynamic(at.Type)
		permission.UnregisterRuleSink(at.Type)
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	defaults := append([]string(nil), m.Policies.Default...)
	policyent.RegisterDefaultPolicy(at.Type, func() any {
		return &policyent.CommandPolicy{Groups: defaults}
	})
	// 测试连接：仅当 describe() 声明了处理器时才登记，且只在这条(而非
	// RegisterDescribeOnly)路径——测试连接要跑 WASM，opsctl 进程没有运行时。
	if at.TestConnection {
		conntest.Register(at.Type, connTestRegistrar.Build(l.name, m, at.Type, l.plugin))
	}
	return nil
}

// validateConfig 把资产即将落库的配置交给 guest 的 validate_config。guest 看到的正是它的
// 工具之后经 ctx.AssetConfig() 读回的那份（password 字段为密文），因此"表单一套规则、
// 工具一套规则"不会分叉。guest 调用失败即拒绝保存：校验没跑成不等于校验通过。
func validateConfig(l loaded) asset_entity.ConfigValidator {
	return func(ctx context.Context, a *asset_entity.Asset) error {
		errs, err := l.plugin.ValidateConfig(ctx, json.RawMessage(a.Config))
		if err != nil {
			return fmt.Errorf("extension %q: validate %s config: %w", l.name, a.Type, err)
		}
		if len(errs) == 0 {
			return nil
		}
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			if e.Field == "" {
				msgs = append(msgs, e.Message)
				continue
			}
			msgs = append(msgs, e.Field+": "+e.Message)
		}
		return fmt.Errorf("invalid %s config: %s", a.Type, strings.Join(msgs, "; "))
	}
}

func unregisterType(assetType string) {
	assettype.Unregister(assetType)
	permission.UnregisterPolicyCheck(assetType)
	permission.UnregisterExecutor(assetType)
	permission.UnregisterRuleSink(assetType)
	skills.UnregisterDynamic(assetType)
	policyent.UnregisterDefaultPolicy(assetType)
	asset_entity.UnregisterConfigValidator(assetType)
	// 无条件调用：未声明测试连接处理器的类型从未在这张表里出现过，Unregister 对它是空操作。
	conntest.Unregister(assetType)
}

// skillDescription 是技能清单里的那一行。优先用 SKILL.md frontmatter 的 description，
// 其次是 manifest 的 i18n 描述——两者都缺时至少说清这个类型归谁，别在清单里留一行空白。
func skillDescription(ext *extension.Extension) string {
	if d := strings.TrimSpace(ext.SkillDescription); d != "" {
		return d
	}
	if d := strings.TrimSpace(ext.Translate(helpLang, ext.Manifest.I18n.Description)); d != "" {
		return d
	}
	return fmt.Sprintf("Asset type provided by the %q extension.", ext.Name)
}

// helpDocument 是 help(asset) 对扩展类型返回的内容：SKILL.md 正文 + 由 manifest
// describe() 报上来的 tools[].parameters 渲染出的工具/参数表。
//
// 这两半各自补对方的洞：SKILL.md 是散文，说得清"这个扩展是干什么的"，但曾经是模型能拿到
// 的**唯一**信息，flag 名与类型只能靠猜；参数表是权威的（同一份声明被 parseCommand 强制
// 执行），但读不出语义。缺 SKILL.md 也照常给出参数表——没有散文不等于没有语法。
func helpDocument(skillMD, extName string, localized *extension.Manifest) string {
	var parts []string
	if body := strings.TrimSpace(skillMD); body != "" {
		parts = append(parts, body)
	}
	if ref := strings.TrimSpace(localized.ToolReference()); ref != "" {
		parts = append(parts, ref)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Asset type provided by extension %q. It declares no tools.", extName)
	}
	return strings.Join(parts, "\n\n")
}

// canonicalize 是注册给 permission 的 CanonicalizeFunc：它在**权限检查之前**跑，因此
// 一条 flag 写错、工具名不存在的命令会在弹出审批框之前失败，而不是让用户先点头、
// 批准之后才发现命令根本调不动。返回的规范串同时是策略匹配、审批展示与 grant 的主体。
func canonicalize(l loaded) permission.CanonicalizeFunc {
	return func(_ *asset_entity.Asset, command string) (string, error) {
		return canonicalCommand(l.manifest, command)
	}
}

// execTool 是注册给 permission 的执行器。它只在权限检查通过之后被调用（统一 exec 的
// 顺序契约），因此这里不再做任何策略判断——那是 policyCheck 的事。
func execTool(l loaded) permission.ExecFunc {
	return func(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
		toolName, argsJSON, err := parseCommand(l.manifest, command)
		if err != nil {
			return "", err
		}
		log := logger.Ctx(ctx).With(zap.String("extension", l.name),
			zap.String("tool", toolName), zap.Int64("assetID", asset.ID))
		log.Info("extension tool execution start")
		// The exec target travels in the call envelope, not in the arguments: it is
		// the asset the policy check above just ran against, so a tool cannot be
		// pointed at a different one by what the model wrote on the command line.
		result, err := l.plugin.CallTool(ctx, toolName, argsJSON,
			&extension.AssetRef{ID: asset.ID, Name: asset.Name, Type: asset.Type})
		if err != nil {
			// 不记 raw error：它可能包装用户输入或远端输出。Error 级别本身即失败状态。
			log.Error("extension tool execution failed")
			return "", fmt.Errorf("%s.%s failed: %w", l.name, toolName, err)
		}
		log.Info("extension tool execution end")
		return string(result), nil
	}
}

// policyCheck 是扩展类型的策略判定，形状与内置类型的 check* 函数一致：
// 类型策略（deny → allow）→ grant → NeedConfirm。
//
// 与内置类型的差别只在中间那一步的语言：内置类型把命令文本拿去撞规则模式，扩展则先问
// guest 的 check_policy 这条调用按参数分类成哪个 (action, resource)，再拿它去撞 holder
// 自身那一列与它引用的权限组里的 `<action>[:<resource-glob>]` 规则
// （policy.CheckExtensionPolicy）。两套引擎不合并，是因为它们判定的根本不是同一种东西。
//
// guest 给出的 action 必须属于类型在 describe() 里声明的动作集合（manifest 的
// policies.actions）。集合外的 action——包括空串、带 ':' 想冒充 "动作:资源" 的串——
// 是 guest 的缺陷：记一条错误，直接 NeedConfirm，既不撞规则也不查 grant，让用户看见
// 这次调用本身。
//
// 返回 NeedConfirm 之后发生什么，则与内置类型完全一致：CheckForAsset 弹审批框，
// "全部允许"落 grant，下一条同样的命令由这里的 MatchGrant 直接放行。
func policyCheck(l loaded, assetType string) permission.PolicyCheckFunc {
	return func(ctx context.Context, assetID int64, command string) aictx.CheckResult {
		action, resource, _, _, ok := classifyCommand(ctx, l, command)
		if !ok {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}
		policyType := l.manifest.Policies.Type
		groups, own := permission.ExtensionPolicyForAsset(ctx, assetID, policyType)
		if len(groups) == 0 {
			groups = l.manifest.Policies.Default
		}
		result := aipolicy.CheckExtensionPolicy(ctx, aipolicy.ExtensionCheck{
			PolicyType: policyType,
			GroupIDs:   groups,
			Own:        own,
			Action:     action,
			Resource:   resource,
		})
		if result.Decision != aictx.NeedConfirm {
			return result
		}
		// Matched by classification (action, resource), not by re-parsing command:
		// two calls that spell the same request differently — different flag order,
		// an equivalent literal — must hit the same grant (spec 参数级策略 › 审批展示).
		if granted, ok := permission.MatchExtensionGrant(ctx, assetID, assetType, policyType, action, resource); ok {
			return granted
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
}

// classifyCommand parses a command and runs the guest's check_policy classification,
// validating the action against the type's declared set (manifest.Policies.Actions).
// It is the single place policyCheck and classifyForApproval both call, so "undeclared
// action never classifies" can't drift between the check path and the approval/grant
// path — both must see the same failure the same way.
func classifyCommand(ctx context.Context, l loaded, command string) (action, resource, toolName string, argsJSON json.RawMessage, ok bool) {
	toolName, argsJSON, err := parseCommand(l.manifest, command)
	if err != nil {
		// 到不了这里：canonicalize 已经用同一个解析器跑过一遍。真发生了就是
		// fail-closed 的"分类失败"，而不是放行或落 grant。
		return "", "", "", nil, false
	}
	action, resource, err = l.plugin.CheckPolicy(ctx, toolName, argsJSON)
	if err != nil {
		logger.Ctx(ctx).Warn("extension policy check failed",
			zap.String("extension", l.name), zap.String("tool", toolName))
		return "", "", "", nil, false
	}
	if !slices.Contains(l.manifest.Policies.Actions, action) {
		logger.Ctx(ctx).Error("extension policy returned an undeclared action",
			zap.String("extension", l.name), zap.String("tool", toolName), zap.String("action", action))
		return "", "", "", nil, false
	}
	return action, resource, toolName, argsJSON, true
}

// classifyForApproval adapts classifyCommand to permission.ClassifyFunc: it is the
// grant-pattern producer HandleConfirm calls to show an approval item's Action/
// Resource/Detail and to build the "always allow" grant key (spec 参数级策略 ›
// 审批展示 — grant persisted as ext:<type>:<action>:<resource>).
func classifyForApproval(l loaded) permission.ClassifyFunc {
	return func(ctx context.Context, command string) (permission.ExtensionClassification, bool) {
		action, resource, toolName, argsJSON, ok := classifyCommand(ctx, l, command)
		if !ok {
			return permission.ExtensionClassification{}, false
		}
		return permission.ExtensionClassification{
			PolicyType: l.manifest.Policies.Type,
			Action:     action,
			Resource:   resource,
			Tool:       toolName,
			Args:       argsJSON,
		}, true
	}
}
