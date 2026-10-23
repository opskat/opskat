package extreg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
)

// 仅描述注册：把一个扩展**不需要 WASM 运行时**的那一半接进宿主注册表。
//
// 它存在是因为 opsctl 启动时要在不编译任何 WASM 的前提下回答"notebook 是个资产类型
// 吗"——否则 `create asset --type notebook` / `help notebook` / `policy show <ext-asset>`
// 全部报"未知类型"。桌面端加载 WASM 模块、问 describe()、再走 Register 把资产类型、
// 执行器、策略判定一并注册；opsctl 启动时只走本路径，每条命令都省掉编译所有扩展的开销。
//
// 缺口的地基是 extension_describe 表：它的存在意义正是"注册资产类型不必跑 WASM"。
// 本文件是消费那份缓存的注册路径。
//
// **注册的与不注册的**，界线就是"这一步要不要调 guest"：
//   - 注册：资产类型（configSchema 决定的字段契约）、help 文档（SKILL.md + 由
//     tools[].parameters 渲染的参数表）、永久规则落点、默认策略与扩展权限组。
//     它们全部由 describe() 的**数据**决定，缓存里就有。
//   - 不注册：执行器与 policyCheck。两者都要调 guest（CallTool / check_policy），
//     需要 WASM 运行时。opsctl 的 exec 要执行时，先用 extension_svc 的 Activate 在本进程里
//     加载目标资产所属的那一个扩展，卸掉这里的仅描述注册，再走 Register 完整注册，
//     之后与内置类型走同一条统一 exec 路径（审批 / grant / 审计）。
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
		Connection:          connectionSpec(at),
	}
}

// connectionSpec 翻译该类型交给宿主接管的连接项。没声明 connection 的类型一项都没有，
// 自动化写入面也就不接受任何宿主连接字段。
func connectionSpec(at extension.AssetTypeDef) assettype.ExtensionConnectionSpec {
	if at.Connection == nil {
		return assettype.ExtensionConnectionSpec{}
	}
	return assettype.ExtensionConnectionSpec{
		ConfigKey:  extension.HostConnectionConfigKey,
		SSHTunnel:  at.Connection.SSHTunnel,
		ProxyChain: at.Connection.ProxyChain,
		TLS:        at.Connection.TLS,
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
		if err := policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:     pg.ID,
			Name:          pg.I18n.Name,
			Description:   pg.I18n.Description,
			PolicyType:    m.Policies.Type,
			Policy:        string(policyJSON),
			ExtensionName: extName,
		}); err != nil {
			return fmt.Errorf("extension %q: %w", extName, err)
		}
	}
	return nil
}

// RegisterDescribeOnly 按缓存的 describe() 答案注册一个扩展的非执行面。
//
// info 由 extension.LoadManifestInfo 读出：manifest 的安全契约 + 缓存里那份描述符 +
// SKILL.md + locales，全程不编译也不实例化 WASM。缓存里没有这个扩展的答案时
// info.Manifest 的资产类型与数据库驱动描述都是空的（describe() 至少声明其一；只提供
// 驱动的扩展没有资产类型，驱动照常注册但没有可运行的会话——打开报驱动不可用，激活后才能执行），
// 本函数报错而不是静默注册零个类型——"什么都没发生"和"注册成功"必须能区分开。调用方（opsctl）
// 在缓存缺失时先自己加载一次扩展，带着取得的 describe() 答案来注册。
//
// 与 Register 一样是全有或全无，也写同一张 registered 表：一个进程里一个扩展只有一种
// 接线方式；这条路没有 plugin，桌面端不会用它。
func RegisterDescribeOnly(info *extension.ManifestInfo) error {
	m := info.Manifest
	if len(m.AssetTypes) == 0 && len(m.DatabaseDrivers) == 0 {
		return fmt.Errorf("extension %q has no cached describe() answer on this machine", info.Name)
	}
	localized := m.Localized(func(key string) string { return info.Translate(helpLang, key) })
	withdrawn, err := registerDescribeOnlyLocked(info, helpDocument(info.SkillMD, info.Name, localized))
	// 同 register：撤回的驱动在 mu 之外作废会话。
	removeDrivers(withdrawn)
	return err
}

// registerDescribeOnlyLocked 持 mu 做 RegisterDescribeOnly 的登记。失败时返回已移出驱动注册表、
// 会话待作废的驱动。
func registerDescribeOnlyLocked(info *extension.ManifestInfo, help string) ([]*extensionDriver, error) {
	m := info.Manifest
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registered[info.Name]; exists {
		return nil, fmt.Errorf("extension %q is already registered", info.Name)
	}
	if err := claimPolicyType(info.Name, m.Policies.Type); err != nil {
		return nil, err
	}

	// 驱动先注册，理由同 register：撞名最常见，先查。
	extDrivers, err := registerDrivers(info.Name, m.DatabaseDrivers, notLoadedOpener{ext: info.Name}, info.Translate)
	if err != nil {
		delete(policyTypeOwner, m.Policies.Type)
		return extDrivers, err
	}

	var done []string
	rollback := func() []*extensionDriver {
		for _, t := range done {
			unregisterType(t)
		}
		policy_group_entity.UnregisterExtensionGroupsByExtension(info.Name)
		delete(policyTypeOwner, m.Policies.Type)
		return releaseDrivers(extDrivers)
	}

	for _, at := range m.AssetTypes {
		if err := registerDescribeOnlyType(info.Name, m, at, help); err != nil {
			return rollback(), err
		}
		done = append(done, at.Type)
	}
	if err := registerPolicyGroups(info.Name, m); err != nil {
		return rollback(), err
	}

	registered[info.Name] = done
	drivers[info.Name] = extDrivers
	manifests[info.Name] = m
	logger.Default().Info("extension registered from cached descriptor",
		zap.String("extension", info.Name), zap.Strings("assetTypes", done),
		zap.Strings("databaseDrivers", m.Capabilities.DatabaseDrivers))
	return nil, nil
}

// notLoadedOpener 是仅描述注册里驱动背后的"插件"：驱动的描述与专属字段已可用，但这个进程没有
// 加载扩展，打开会话与切分语句都报驱动不可用。需要执行的调用方先激活扩展（Unregister + 完整 Register）。
type notLoadedOpener struct{ ext string }

func (o notLoadedOpener) OpenDriverSession(_ context.Context, id string, _ dbdriver.OpenRequest) (dbdriver.Session, error) {
	return nil, o.unavailable(id)
}

func (o notLoadedOpener) ClassifyStatements(_ context.Context, id, _ string) ([]dbdriver.Statement, error) {
	return nil, o.unavailable(id)
}

func (o notLoadedOpener) unavailable(id string) error {
	return fmt.Errorf("%w: %q (its extension %q is not loaded in this process)", dbregistry.ErrDriverUnavailable, id, o.ext)
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

// DriverOwnerOf 回答已注册的数据库驱动 id 由哪个扩展提供；内置驱动与未注册的 id 报 false。
func DriverOwnerOf(id dbdriver.DriverID) (string, bool) {
	mu.Lock()
	defer mu.Unlock()
	ext, ok := driverOwner[id]
	return ext, ok
}
