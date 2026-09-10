package extreg

import (
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/pkg/extension"
)

// 仅描述注册：把一个扩展**不需要 WASM 运行时**的那一半接进宿主注册表。
//
// 它存在是因为 opsctl 是另一个进程。桌面端加载 WASM 模块、问 describe()、再走
// Register 把资产类型、执行器、策略判定一并注册；opsctl 没有 wazero、没有解密后的
// 资产配置、也没有审批 UI，却仍然要能回答"notebook 是个资产类型吗"——否则
// `create asset --type notebook` / `help notebook` / `policy show <ext-asset>` 全部
// 报"未知类型"，而同一台机器上的桌面端明明认识它。
//
// 缺口的地基是 extension_describe 表：它的存在意义正是"注册资产类型不必跑 WASM"。
// 本文件是消费那份缓存的注册路径。
//
// **注册的与不注册的**，界线就是"这一步要不要调 guest"：
//   - 注册：资产类型（configSchema 决定的字段契约）、help 文档（SKILL.md + 由
//     tools[].parameters 渲染的参数表）、永久规则落点、默认策略与扩展权限组。
//     它们全部由 describe() 的**数据**决定，缓存里就有。
//   - 不注册：执行器与 policyCheck。两者都要调 guest（CallTool / check_policy），
//     而 WASM 运行时、宿主能力面与凭据都只在桌面进程里。opsctl 的 exec 因此继续把
//     命令交回桌面端执行（cmd/opsctl/command/exec.go 的 execViaDesktop），
//     策略/审批/grant/审计由那一端按同一条统一 exec 路径落库。
//
// 这不是"opsctl 专用的第二套注册"：spec 推导、help 渲染、权限组落库都与 Register
// 共用同一份代码，本文件只是少调用几个注册函数。

// extensionTypeSpec 把 describe() 报上来的一个资产类型翻译成 assettype 的注册声明。
// 两条注册路径（Register / RegisterDescribeOnly）共用它，否则 configSchema →
// 资产类型的契约会有两份，其中一份迟早落后。
func extensionTypeSpec(extName string, m *extension.Manifest, at extension.AssetTypeDef) assettype.ExtensionTypeSpec {
	return assettype.ExtensionTypeSpec{
		Type:                at.Type,
		ExtensionName:       extName,
		ConfigFields:        extension.ConfigSchemaProperties(at.ConfigSchema),
		RequiredFields:      extension.ConfigSchemaRequired(at.ConfigSchema),
		SecretFields:        extension.PasswordFieldsFromSchema(at.ConfigSchema),
		PolicyKind:          m.Policies.Type,
		DefaultPolicyGroups: m.Policies.Default,
	}
}

// registerPolicyGroups 把扩展声明的权限组登记进内置权限组表，同样为两条路径共用：
// 一个扩展资产的默认策略只引用组 ID，组本身不落库，不登记 `policy show` 就解析不出
// 它们的规则。
func registerPolicyGroups(extName string, m *extension.Manifest) error {
	for _, pg := range m.Policies.Groups {
		policyJSON, err := json.Marshal(pg.Policy)
		if err != nil {
			return fmt.Errorf("extension %q policy group %q: %w", extName, pg.ID, err)
		}
		policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:     pg.ID,
			Name:          pg.I18n.Name,
			Description:   pg.I18n.Description,
			PolicyType:    m.Policies.Type,
			Policy:        string(policyJSON),
			ExtensionName: extName,
		})
	}
	return nil
}

// RegisterDescribeOnly 按缓存的 describe() 答案注册一个扩展的非执行面。
//
// info 由 extension.LoadManifestInfo 读出：manifest 的安全契约 + 缓存里那份描述符 +
// SKILL.md + locales，全程不编译也不实例化 WASM。缓存里没有这个扩展（这台机器从没
// 加载过它）时 info.Manifest.AssetTypes 是空的，本函数报错而不是静默注册零个类型——
// "什么都没发生"和"注册成功"必须能区分开，调用方据此告诉用户去桌面端加载一次。
//
// 与 Register 一样是全有或全无，也写同一张 registered 表：一个进程里一个扩展只有一种
// 接线方式，桌面端不会用这条路，opsctl 也拿不到 plugin。
func RegisterDescribeOnly(info *extension.ManifestInfo) error {
	m := info.Manifest
	if len(m.AssetTypes) == 0 {
		return fmt.Errorf("extension %q has no cached describe() answer on this machine: "+
			"its asset types cannot be registered until the desktop app loads it once", info.Name)
	}
	localized := m.Localized(func(key string) string { return info.Translate(helpLang, key) })
	help := helpDocument(info.SkillMD, info.Name, localized)

	mu.Lock()
	defer mu.Unlock()
	if _, exists := registered[info.Name]; exists {
		return fmt.Errorf("extension %q is already registered", info.Name)
	}

	var done []string
	rollback := func() {
		for _, t := range done {
			unregisterType(t)
		}
		policy_group_entity.UnregisterExtensionGroupsByExtension(info.Name)
	}

	for _, at := range m.AssetTypes {
		if err := registerDescribeOnlyType(info.Name, m, at, help); err != nil {
			rollback()
			return err
		}
		done = append(done, at.Type)
	}
	if err := registerPolicyGroups(info.Name, m); err != nil {
		rollback()
		return err
	}

	registered[info.Name] = done
	logger.Default().Info("extension registered from cached descriptor",
		zap.String("extension", info.Name), zap.Strings("assetTypes", done))
	return nil
}

func registerDescribeOnlyType(extName string, m *extension.Manifest, at extension.AssetTypeDef, help string) error {
	if err := assettype.RegisterExtensionType(extensionTypeSpec(extName, m, at)); err != nil {
		return fmt.Errorf("extension %q: %w", extName, err)
	}
	// 文档而非执行器：HelpFor 因此照常返回这份用法文档，而 ExecutorFor /
	// RegisteredExecTypes 跳过它——这个进程确实执行不了，不能假装能。
	if err := permission.RegisterDynamicHelpDoc(at.Type, help); err != nil {
		assettype.Unregister(at.Type)
		return fmt.Errorf("extension %q: %w", extName, err)
	}
	if err := permission.RegisterExtensionRuleSink(at.Type, m.Policies.Type, m.Policies.Actions); err != nil {
		assettype.Unregister(at.Type)
		permission.UnregisterExecutor(at.Type)
		return fmt.Errorf("extension %q: %w", extName, err)
	}
	defaults := append([]string(nil), m.Policies.Default...)
	policyent.RegisterDefaultPolicy(at.Type, func() any {
		return &policyent.CommandPolicy{Groups: defaults}
	})
	return nil
}
