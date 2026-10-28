package extreg

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// nameRe 是资产类型名与策略面名的形状：二者都是命名空间段（权限组 ID
// ext:<policyType>:<name>、grant ext:<policyType>:<action>:<resource>），一个能自带 ":" 的
// 名字会让一个命名空间拼出另一个的。
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// headerNameRe is an RFC 7230 token: what net/http accepts as a header name.
var headerNameRe = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// assetTypesPoint 是资产类型的贡献点：configSchema、端点字段、认证绑定、连接项的规则，
// 接入是资产类型注册表（internal/assettype）、资产写入口的 guest 配置校验与测试连接。
type assetTypesPoint struct{ guest guestFunc }

func (assetTypesPoint) Key() string { return extwire.KeyAssetTypes }

// Check 校验每个资产类型。认证与连接项只作用于资产的端点：类型里得有 format:"endpoint"
// 字段，manifest 里得有 permissions.network.assetEndpoint——否则它们会在表单里出现却永不生效。
func (assetTypesPoint) Check(v extension.ExtensionView) error {
	var types []extwire.AssetType
	if err := decodeContribution(v, extwire.KeyAssetTypes, &types); err != nil {
		return err
	}
	assetEndpoint := v.Manifest().Permissions.Network.AssetEndpoint
	seen := make(map[string]struct{}, len(types))
	for i, at := range types {
		if at.Type == "" {
			return fmt.Errorf("assetTypes[%d]: type is required", i)
		}
		if !nameRe.MatchString(at.Type) {
			return fmt.Errorf("assetTypes[%d]: type must match %s (got %q)", i, nameRe.String(), at.Type)
		}
		if _, dup := seen[at.Type]; dup {
			return fmt.Errorf("assetTypes[%s]: duplicate asset type", at.Type)
		}
		seen[at.Type] = struct{}{}
		if err := checkAssetType(at, assetEndpoint); err != nil {
			return fmt.Errorf("assetTypes[%s]: %w", at.Type, err)
		}
	}
	return nil
}

func checkAssetType(at extwire.AssetType, assetEndpoint bool) error {
	props := extension.ConfigSchemaProperties(at.ConfigSchema)
	if len(props) == 0 {
		return fmt.Errorf("configSchema must declare properties")
	}
	if err := checkEnumPresentation(at.ConfigSchema); err != nil {
		return fmt.Errorf("configSchema.%w", err)
	}
	binds := bindsEndpoint(at)
	if binds && len(extension.EndpointFieldsFromSchema(at.ConfigSchema)) == 0 {
		return fmt.Errorf(`declares auth or connection, which apply only to the asset's endpoint, but its configSchema marks no field format:"endpoint"`)
	}
	if at.Auth != nil {
		declared := make(map[string]bool, len(props))
		for _, p := range props {
			declared[p] = true
		}
		secrets := make(map[string]bool)
		for _, p := range extension.PasswordFieldsFromSchema(at.ConfigSchema) {
			secrets[p] = true
		}
		if err := checkAuth(at.Auth, declared, secrets); err != nil {
			return err
		}
	}
	if binds && !assetEndpoint {
		return fmt.Errorf("declares auth or connection, which apply only to the asset's endpoint, but the manifest does not declare permissions.network.assetEndpoint")
	}
	return nil
}

// bindsEndpoint reports whether the type declares anything the host applies only
// on a connection to the asset's endpoint: credential injection or a connection
// setting.
func bindsEndpoint(at extwire.AssetType) bool {
	c := at.Connection
	return at.Auth != nil || (c != nil && (c.SSHTunnel || c.ProxyChain || c.TLS))
}

// checkEnumPresentation checks the presentation keys a config field may add to its
// enum: enumLabels (one display label per option, in option order) and default (one
// of the options). The form indexes labels by option position and preselects the
// default, so a mismatch would mislabel or preselect a value the guest rejects.
func checkEnumPresentation(schema map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for _, name := range extension.ConfigSchemaProperties(schema) {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		enum, hasEnum := prop["enum"].([]any)
		if labels, ok := prop["enumLabels"]; ok {
			list, isList := labels.([]any)
			if !hasEnum {
				return fmt.Errorf("properties[%q].enumLabels requires enum", name)
			}
			if !isList || len(list) != len(enum) {
				return fmt.Errorf("properties[%q].enumLabels must be an array with one label per enum option (%d)", name, len(enum))
			}
			for _, l := range list {
				if _, ok := l.(string); !ok {
					return fmt.Errorf("properties[%q].enumLabels must contain only strings", name)
				}
			}
		}
		if def, ok := prop["default"]; ok && hasEnum {
			if !slices.Contains(enum, def) {
				return fmt.Errorf("properties[%q].default %v is not one of its enum options", name, def)
			}
		}
	}
	return nil
}

// checkAuth checks the declaration against the fields the asset type's configSchema
// declares — a template may reference nothing else — and the subset of them that
// are secrets (format:"password"), which may be injected but never select a group:
// the selector's value is compared and logged as plain data.
func checkAuth(a *extwire.Auth, fields, secrets map[string]bool) error {
	if len(a.Groups) == 0 {
		return fmt.Errorf("auth.groups must declare at least one group")
	}
	if a.Selector == "" {
		if len(a.Groups) > 1 {
			return fmt.Errorf("auth declares %d groups but no selector — name the config field that picks one", len(a.Groups))
		}
		if a.Groups[0].When != "" {
			return fmt.Errorf("auth.groups[0].when %q needs a selector field to compare against", a.Groups[0].When)
		}
	} else if !fields[a.Selector] {
		return fmt.Errorf("auth.selector %q is not a configSchema property", a.Selector)
	} else if secrets[a.Selector] {
		return fmt.Errorf("auth.selector %q is a password field; a secret cannot select an auth group", a.Selector)
	}
	seen := make(map[string]bool, len(a.Groups))
	for i, g := range a.Groups {
		if a.Selector != "" {
			if g.When == "" {
				return fmt.Errorf("auth.groups[%d].when is required: it is the %s value that selects the group", i, a.Selector)
			}
			if seen[g.When] {
				return fmt.Errorf("auth.groups: duplicate when %q", g.When)
			}
			seen[g.When] = true
		}
		if len(g.Bindings) == 0 {
			return fmt.Errorf("auth.groups[%d].bindings must declare at least one binding", i)
		}
		for j, b := range g.Bindings {
			if err := checkAuthBinding(b, fields); err != nil {
				return fmt.Errorf("auth.groups[%d].bindings[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func checkAuthBinding(b extwire.AuthBinding, fields map[string]bool) error {
	switch b.In {
	case extension.AuthInHeader:
		if !headerNameRe.MatchString(b.Name) {
			return fmt.Errorf("header binding needs a valid header name (got %q)", b.Name)
		}
	case extension.AuthInQuery:
		if b.Name == "" {
			return fmt.Errorf("query binding needs a parameter name")
		}
	case extension.AuthInBasic:
		if b.Name != "" {
			return fmt.Errorf("basic binding takes no name (got %q): it always sets Authorization", b.Name)
		}
	default:
		return fmt.Errorf("in must be header, query or basic (got %q)", b.In)
	}
	refs, err := extension.AuthTemplateFields(b.Value)
	if err != nil {
		return err
	}
	for _, f := range refs {
		if !fields[f] {
			return fmt.Errorf("template references %q, which is not a configSchema property", f)
		}
	}
	return nil
}

// Attach 把每个资产类型登记进资产类型注册表；有运行中的插件时，guest 的配置校验挂到资产
// 写入口，声明了测试连接处理器的类型登记测试连接。仅描述接入（opsctl 启动时，没有 WASM）
// 只有 configSchema 的字段契约。
func (p assetTypesPoint) Attach(v extension.ExtensionView) (func(), error) {
	l := p.guest.loaded(v)
	var done undo
	for _, at := range l.manifest.AssetTypes {
		revoke, err := attachAssetType(l, at)
		if err != nil {
			done.run()
			return nil, fmt.Errorf("assetTypes[%s]: %w", at.Type, err)
		}
		done = append(done, revoke)
	}
	return done.run, nil
}

func attachAssetType(l loaded, at extension.AssetTypeDef) (func(), error) {
	// 测试连接要跑 WASM，只有加载扩展的进程（桌面端、激活扩展的 opsctl）接了 registrar；先查，免得登记到一半再回滚。
	if l.plugin != nil && at.TestConnection && connTestRegistrar == nil {
		return nil, fmt.Errorf("declares a test-connection handler but no registrar is wired")
	}
	if err := assettype.RegisterExtensionType(extensionTypeSpec(l.name, l.manifest, at)); err != nil {
		return nil, err
	}
	done := undo{func() { assettype.Unregister(at.Type) }}
	if l.plugin == nil {
		return done.run, nil
	}
	// 配置校验：guest 的 RegisterConfigValidator 挂到资产写入口（asset_svc），桌面表单、
	// put_asset、opsctl create 落库前都经它。仅描述接入没有 plugin，不注册——那时只有
	// configSchema 的必填/未知字段校验；opsctl 激活扩展后走完整接入，才有 guest 校验。
	if err := asset_entity.RegisterConfigValidator(at.Type, validateConfig(l)); err != nil {
		done.run()
		return nil, err
	}
	done = append(done, func() { asset_entity.UnregisterConfigValidator(at.Type) })
	// 测试连接：仅当 describe() 声明了处理器时才登记，且只在有运行时的接入里。
	if at.TestConnection {
		conntest.Register(at.Type, connTestRegistrar.Build(l.name, l.manifest, at.Type, l.plugin))
		done = append(done, func() { conntest.Unregister(at.Type) })
	}
	return done.run, nil
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

// extensionTypeSpec 把 describe() 报上来的一个资产类型翻译成 assettype 的注册声明。
// 完整接入与仅描述接入共用它，否则 configSchema → 资产类型的契约会有两份，其中一份迟早落后。
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

// AssetConnection 是宿主替扩展类型资产解析好的连接路径：拨号（nil 为直连）、TLS 设置与二者
// 所据设置的指纹。
type AssetConnection struct {
	Dial        extension.DialContextFunc
	TLS         *tls.Config
	Fingerprint string
}

// AssetScope 是对 at 类型资产 a 的一次调用的作用域：资产类型这一侧据此告诉运行时这次调用能
// 到达什么——config 里 format:"endpoint" 字段写的地址是端点，connect 构建连接路径，at 声明的
// 认证分组按 creds 渲染成认证注入。运行时只按作用域与权限放行，不再自己读 configSchema 与认证
// 声明。config 是资产存着的配置（端点字段不是口令，不必解密）。连接路径只在流真要走它（去端点、
// 经 assetRoute）时才由运行时调 connect 构建（Scope.Connect）：白名单请求照旧直连，路径建不起来
// （CA 文件没了、代理链解析失败）也不影响它。
func AssetScope(a extension.AssetRef, at *extension.AssetTypeDef, config json.RawMessage, connect func(context.Context) (AssetConnection, error), creds extension.CredentialValues) (*extension.Scope, error) {
	endpoints, err := assetEndpoints(at, config)
	if err != nil {
		return nil, fmt.Errorf("asset type %s: %w", at.Type, err)
	}
	scope := &extension.Scope{
		Asset:     &a,
		Endpoints: endpoints,
		Connect: func(ctx context.Context) (extension.Connection, error) {
			conn, err := connect(ctx)
			if err != nil {
				return extension.Connection{}, err
			}
			route := &extension.Route{Dial: conn.Dial}
			if route.Dial == nil {
				route.Dial = (&netdial.Dialer{}).DialContext
				route.Direct = true
			}
			return extension.Connection{Route: route, TLS: conn.TLS, Fingerprint: conn.Fingerprint}, nil
		},
	}
	if at.Auth != nil {
		scope.Auth = at.Auth.Inject(creds)
	}
	return scope, nil
}

// assetEndpoints 读出 config 里端点字段写的地址；字段缺失、不是字符串或写不出端口的不算端点。
func assetEndpoints(at *extension.AssetTypeDef, config json.RawMessage) ([]extension.Endpoint, error) {
	fields := extension.EndpointFieldsFromSchema(at.ConfigSchema)
	if len(fields) == 0 || len(config) == 0 {
		return nil, nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	var endpoints []extension.Endpoint
	for _, field := range fields {
		var value string
		if json.Unmarshal(cfg[field], &value) != nil {
			continue
		}
		if ep, ok := extension.ParseEndpoint(value); ok {
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints, nil
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
