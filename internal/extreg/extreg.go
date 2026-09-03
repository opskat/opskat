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
	"github.com/opskat/opskat/pkg/extension"
)

// helpLang 是渲染扩展用法文档时解析 i18n key 用的语言。文档在加载期渲染一次并注册进
// permission，而模型的对话语言是会话期才知道的；内置类型的 SKILL.md 同样只有英文。
const helpLang = "en"

// pluginCaller 是本包用到的 WASM 插件能力子集（*extension.Plugin 满足它）。
// 收窄到两个方法而不是直接吃 *Plugin，是为了让策略/执行这两条闭包可以在没有 wazero
// 运行时的情况下被测试驱动——它们承载的是策略与 grant 的行为契约，不是 WASM 加载。
type pluginCaller interface {
	CallTool(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error)
	CheckPolicy(ctx context.Context, toolName string, args json.RawMessage) (action, resource string, err error)
}

// loaded 是一个已加载扩展在本包内的最小画像。
type loaded struct {
	name     string
	manifest *extension.Manifest
	plugin   pluginCaller
}

var (
	mu         sync.Mutex
	registered = make(map[string][]string) // extension name → registered asset types
)

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

	var done []string
	rollback := func() {
		for _, t := range done {
			unregisterType(t)
		}
		policy_group_entity.UnregisterExtensionGroupsByExtension(l.name)
	}

	for _, at := range m.AssetTypes {
		if err := registerType(l, at, help, description); err != nil {
			rollback()
			return err
		}
		done = append(done, at.Type)
	}

	for _, pg := range m.Policies.Groups {
		policyJSON, err := json.Marshal(pg.Policy)
		if err != nil {
			rollback()
			return fmt.Errorf("extension %q policy group %q: %w", l.name, pg.ID, err)
		}
		policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:     pg.ID,
			Name:          pg.I18n.Name,
			Description:   pg.I18n.Description,
			PolicyType:    m.Policies.Type,
			Policy:        string(policyJSON),
			ExtensionName: l.name,
		})
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
	delete(registered, name)
	logger.Default().Info("extension unregistered",
		zap.String("extension", name), zap.Strings("assetTypes", types))
}

func registerType(l loaded, at extension.AssetTypeDef, help, description string) error {
	m := l.manifest
	spec := assettype.ExtensionTypeSpec{
		Type:                at.Type,
		ExtensionName:       l.name,
		ConfigFields:        extension.ConfigSchemaProperties(at.ConfigSchema),
		RequiredFields:      extension.ConfigSchemaRequired(at.ConfigSchema),
		SecretFields:        extension.PasswordFieldsFromSchema(at.ConfigSchema),
		PolicyKind:          m.Policies.Type,
		DefaultPolicyGroups: m.Policies.Default,
	}
	if err := assettype.RegisterExtensionType(spec); err != nil {
		return fmt.Errorf("extension %q: %w", l.name, err)
	}
	if err := permission.RegisterPolicyCheck(at.Type, policyCheck(l, at.Type)); err != nil {
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
	defaults := append([]string(nil), m.Policies.Default...)
	policyent.RegisterDefaultPolicy(at.Type, func() any {
		return &policyent.CommandPolicy{Groups: defaults}
	})
	return nil
}

func unregisterType(assetType string) {
	assettype.Unregister(assetType)
	permission.UnregisterPolicyCheck(assetType)
	permission.UnregisterExecutor(assetType)
	skills.UnregisterDynamic(assetType)
	policyent.UnregisterDefaultPolicy(assetType)
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
		result, err := l.plugin.CallTool(ctx, toolName, argsJSON)
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
// 类型策略 → grant → NeedConfirm。
//
// 与内置类型的差别只在中间那一步的语言：内置类型把命令文本拿去撞规则模式，扩展则先问
// guest 的 check_policy 这条调用请求的是哪个 action，再拿 action 去撞权限组的精确
// allow/deny 名单（policy.CheckExtensionPolicy）。两套引擎不合并，是因为它们判定的
// 根本不是同一种东西。
//
// 返回 NeedConfirm 之后发生什么，则与内置类型完全一致：CheckForAsset 弹审批框，
// "全部允许"落 grant，下一条同样的命令由这里的 MatchGrant 直接放行。
func policyCheck(l loaded, assetType string) permission.PolicyCheckFunc {
	return func(ctx context.Context, assetID int64, command string) aictx.CheckResult {
		toolName, argsJSON, err := parseCommand(l.manifest, command)
		if err != nil {
			// 到不了这里：canonicalize 已经用同一个解析器跑过一遍。真发生了就是
			// fail-closed 的 NeedConfirm，而不是放行。
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}
		action, _, err := l.plugin.CheckPolicy(ctx, toolName, argsJSON)
		if err != nil {
			logger.Ctx(ctx).Warn("extension policy check failed",
				zap.String("extension", l.name), zap.String("tool", toolName))
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}
		if action != "" {
			groups := permission.PolicyGroupsForAsset(ctx, assetID)
			if len(groups) == 0 {
				groups = l.manifest.Policies.Default
			}
			result := aipolicy.CheckExtensionPolicy(ctx, groups, action)
			if result.Decision != aictx.NeedConfirm {
				return result
			}
		}
		if granted, ok := permission.MatchGrant(ctx, assetID, command, assetType); ok {
			return granted
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
}
