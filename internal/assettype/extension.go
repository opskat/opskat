package assettype

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// ExtensionTypeSpec 是一个扩展提供的资产类型的声明，由 manifest 推导而来。
//
// 它是**纯数据**，因此本包不 import pkg/extension：manifest → spec 的翻译属于扩展侧
// （internal/extreg），资产类型的行为属于本包。这样内置类型与扩展类型走的是同一张
// 注册表、同一个 AssetTypeHandler 接口，消费点不需要第二条路径。
type ExtensionTypeSpec struct {
	// Type 是资产类型名，与 manifest assetTypes[].type 一致。
	Type string
	// ExtensionName 是拥有该类型的扩展名，写进 asset.extension_name。
	ExtensionName string
	// ConfigFields 是 configSchema.properties 的全部属性名。
	ConfigFields []string
	// RequiredFields 是 configSchema.required 声明的必填属性名。
	RequiredFields []string
	// SecretFields 是 configSchema 里 format:"password" 的属性名：它们落库前加密，
	// 且不进 SafeView / 审批视图。
	SecretFields []string
	// PolicyKind 是 manifest policies.type，写进 entity/policy 的 asset-kind 映射。
	PolicyKind string
	// DefaultPolicyGroups 是 manifest policies.default 声明的默认权限组 ID。
	DefaultPolicyGroups []string
	// Connection 是该类型声明交给宿主接管的连接项。
	Connection ExtensionConnectionSpec
}

// ExtensionConnectionSpec 是 manifest assetTypes[].connection 的翻译：声明了哪一项，
// 自动化写入面（opsctl / put_asset）就多接受对应的宿主连接字段（见 connection_args.go），
// 字段名与内置类型一致。
type ExtensionConnectionSpec struct {
	// ConfigKey 是资产 Config JSON 里宿主保留的那个键
	// （pkg/extension.HostConnectionConfigKey），代理链与 TLS 存在它下面。
	ConfigKey  string
	SSHTunnel  bool
	ProxyChain bool
	TLS        bool
}

// RegisterExtensionType 按 spec 注册一个由 manifest 驱动的资产类型处理器。
// 类型名冲突（另一个扩展或某个内置类型已经占用了它）返回错误。
func RegisterExtensionType(spec ExtensionTypeSpec) error {
	if spec.Type == "" {
		return fmt.Errorf("extension asset type requires a type name")
	}
	if len(spec.ConfigFields) == 0 {
		return fmt.Errorf("extension asset type %q declares no configSchema properties", spec.Type)
	}
	hostFields, _ := extensionConnectionFields(spec.Connection)
	for _, field := range spec.ConfigFields {
		if slices.Contains(hostFields, field) {
			return fmt.Errorf("extension asset type %q declares configSchema property %q, which is the host's own connection field for a connection item the type opts into", spec.Type, field)
		}
	}
	return RegisterDynamic(newExtensionHandler(spec))
}

// extensionConnectionFields 返回该类型声明的连接项换来的宿主连接字段：全部字段，以及其中
// 可进审批视图的部分。
func extensionConnectionFields(conn ExtensionConnectionSpec) (config, approval []string) {
	if conn.SSHTunnel {
		config = append(config, argSSHAsset)
		approval = append(approval, argSSHAsset)
	}
	if conn.ProxyChain {
		config = append(config, argProxyChain)
		approval = append(approval, argProxyChain)
	}
	if conn.TLS {
		config = append(config, tlsConfigArgs()...)
		approval = append(approval, tlsApprovalArgs()...)
	}
	return config, approval
}

type extensionHandler struct {
	spec       ExtensionTypeSpec
	secrets    map[string]struct{}
	required   []string
	config     []string
	approval   []string
	connection map[string]struct{}
}

func newExtensionHandler(spec ExtensionTypeSpec) *extensionHandler {
	secrets := make(map[string]struct{}, len(spec.SecretFields))
	for _, f := range spec.SecretFields {
		secrets[f] = struct{}{}
	}
	approval := make([]string, 0, len(spec.ConfigFields))
	for _, f := range spec.ConfigFields {
		if _, secret := secrets[f]; !secret {
			approval = append(approval, f)
		}
	}
	hostConfig, hostApproval := extensionConnectionFields(spec.Connection)
	connection := make(map[string]struct{}, len(hostConfig))
	for _, f := range hostConfig {
		connection[f] = struct{}{}
	}
	return &extensionHandler{
		spec:       spec,
		secrets:    secrets,
		required:   sortedUnique(spec.RequiredFields),
		config:     sortedUnique(withFields(spec.ConfigFields, hostConfig...)),
		approval:   sortedUnique(withFields(approval, hostApproval...)),
		connection: connection,
	}
}

func (h *extensionHandler) Type() string { return h.spec.Type }

// DefaultPort 扩展资产的连接细节完全由 configSchema 描述，没有宿主侧的默认端口概念。
func (h *extensionHandler) DefaultPort() int { return 0 }

// ExtensionName 让宿主侧在不认识具体类型的情况下回答"这个类型归谁"。
func (h *extensionHandler) ExtensionName() string { return h.spec.ExtensionName }

type extensionOwned interface {
	ExtensionName() string
}

// ExtensionOwnerOf 回答"这个资产类型归哪个扩展"。内置类型返回 ("", false)——注册表
// 拒绝让扩展占用一个已存在的类型名（RegisterExtensionType 冲突即失败），所以本函数
// 同时也是"这个类型必须交给扩展处理吗"的答案。
//
// 它是一个注册表查询而不是调用方各自维护的一张 类型→扩展 表：opsctl 曾经为了给 exec
// 选路自己扫一遍扩展目录再拼一张，那张表与本注册表是两个会分叉的真相来源。
func ExtensionOwnerOf(assetType string) (string, bool) {
	h, ok := Get(assetType)
	if !ok {
		return "", false
	}
	owner, ok := h.(extensionOwned)
	if !ok {
		return "", false
	}
	return owner.ExtensionName(), true
}

// SafeView 返回去掉 format:"password" 字段之后的配置。配置解析失败原样报空 map，
// 与其他类型的 SafeView 一致（该方法没有 error 返回位）。
func (h *extensionHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg := h.decodeConfig(a)
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if _, secret := h.secrets[k]; secret {
			continue
		}
		out[k] = v
	}
	return out
}

// ResolvePassword 扩展资产的密文字段由 WASM 宿主接口按 capabilities.credentials 逐个
// 发放（见 internal/app/extension/host.go），不存在"这个资产的密码"这样一个单值。
func (h *extensionHandler) ResolvePassword(_ context.Context, _ *asset_entity.Asset) (string, error) {
	return "", fmt.Errorf("asset type %q is provided by extension %q; credentials are resolved through the extension host API",
		h.spec.Type, h.spec.ExtensionName)
}

// DefaultPolicy 新建资产时落库的默认策略：只引用 manifest 声明的默认权限组，
// 不自带 allow/deny 规则——扩展的规则语言是 action 名，由权限组持有。
func (h *extensionHandler) DefaultPolicy() any {
	return &policy.CommandPolicy{Groups: append([]string(nil), h.spec.DefaultPolicyGroups...)}
}

func (h *extensionHandler) PolicyKind() string { return h.spec.PolicyKind }

// AutomationContract 按 configSchema 校验字段名与必填项，外加该类型声明的连接项对应的
// 宿主连接字段（ssh_asset_id / proxy_chain / tls*）。不支持托管凭证（CredentialPlan 恒为
// None、BindCredential 为 nil，因此 put_asset 传 credential_id 会被 rejectUnknownFields
// 挡在门外，除非 schema 自己声明了这个字段）。
func (h *extensionHandler) AutomationContract() AutomationContract {
	return newAutomationContract(h.config, h.approval, nil, nil, nil)
}

func (h *extensionHandler) ValidateCreateArgs(args map[string]any) error {
	missing := make([]string, 0)
	for _, field := range h.required {
		if _, ok := args[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required parameters: %v", missing)
}

func (h *extensionHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	return h.apply(a, map[string]any{}, args)
}

func (h *extensionHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	return h.apply(a, h.decodeConfig(a), args)
}

// apply 把 args 分成两半落库：扩展自己的 configSchema 字段进 Config 顶层，宿主连接字段进
// SSHTunnelID 列与 Config 的宿主保留键——扩展永远看不到后者。
func (h *extensionHandler) apply(a *asset_entity.Asset, base, args map[string]any) error {
	own := make(map[string]any, len(args))
	conn := make(map[string]any)
	for k, v := range args {
		if _, ok := h.connection[k]; ok {
			conn[k] = v
			continue
		}
		own[k] = v
	}
	cfg, err := h.encodeConfig(base, own)
	if err != nil {
		return err
	}
	if err := h.applyConnection(a, cfg, conn); err != nil {
		return err
	}
	a.ExtensionName = h.spec.ExtensionName
	return h.storeConfig(a, cfg)
}

// applyConnection 把宿主连接字段写成资产表单存的那份结构（asset_entity.ExtensionConnectionConfig），
// 与已存的合并：只动本次给出的字段。
func (h *extensionHandler) applyConnection(a *asset_entity.Asset, cfg, args map[string]any) error {
	if len(args) == 0 {
		return nil
	}
	key := h.spec.Connection.ConfigKey
	var conn asset_entity.ExtensionConnectionConfig
	if stored, ok := cfg[key]; ok {
		raw, err := json.Marshal(stored)
		if err != nil {
			return fmt.Errorf("encode %s connection config: %w", h.spec.Type, err)
		}
		if err := json.Unmarshal(raw, &conn); err != nil {
			return fmt.Errorf("parse %s connection config: %w", h.spec.Type, err)
		}
	}
	if _, ok := args[argSSHAsset]; ok {
		a.SSHTunnelID = ArgInt64(args, argSSHAsset)
	}
	if err := applyProxyChainArg(a, args, &conn.ProxyChain); err != nil {
		return err
	}
	if err := applyExtensionTLSArgs(args, &conn); err != nil {
		return err
	}
	if conn.ProxyChain == nil && conn.TLS == nil {
		delete(cfg, key)
		return nil
	}
	cfg[key] = conn
	return nil
}

func applyExtensionTLSArgs(args map[string]any, conn *asset_entity.ExtensionConnectionConfig) error {
	tls := asset_entity.ExtensionTLSConfig{}
	if conn.TLS != nil {
		tls = *conn.TLS
	}
	if _, ok := args[argTLS]; ok {
		tls.Enabled = ArgBool(args, argTLS)
	}
	if _, ok := args[argTLSInsec]; ok {
		tls.Insecure = ArgBool(args, argTLSInsec)
	}
	if _, ok := args[argTLSServer]; ok {
		tls.ServerName = ArgString(args, argTLSServer)
	}
	if err := applyTLSCertArgs(args, tlsCertStore{
		caFile: &tls.CAFile, certFile: &tls.CertFile, keyFile: &tls.KeyFile,
		caPEM: &tls.CACert, certPEM: &tls.ClientCert, keyPEM: &tls.ClientKey,
	}); err != nil {
		return err
	}
	if tls == (asset_entity.ExtensionTLSConfig{}) {
		conn.TLS = nil
		return nil
	}
	conn.TLS = &tls
	return nil
}

// encodeConfig 把 args 合并进 base，并把 secret 字段加密。已加密的存量值不会被重复
// 加密：只有本次 args 里出现的字段才走 Encrypt。
func (h *extensionHandler) encodeConfig(base, args map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(base)+len(args))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range args {
		if _, secret := h.secrets[k]; secret {
			plaintext, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("config field %q must be a string", k)
			}
			if plaintext == "" {
				continue
			}
			encrypted, err := credential_svc.Default().Encrypt(plaintext)
			if err != nil {
				return nil, fmt.Errorf("encrypt config field %q: %w", k, err)
			}
			out[k] = encrypted
			continue
		}
		out[k] = v
	}
	return out, nil
}

func (h *extensionHandler) storeConfig(a *asset_entity.Asset, cfg map[string]any) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode %s config: %w", h.spec.Type, err)
	}
	a.Config = string(data)
	return nil
}

func (h *extensionHandler) decodeConfig(a *asset_entity.Asset) map[string]any {
	if a == nil || a.Config == "" {
		return map[string]any{}
	}
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(a.Config), &cfg); err != nil {
		return map[string]any{}
	}
	return cfg
}
