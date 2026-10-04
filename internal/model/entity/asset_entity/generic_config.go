package asset_entity

import (
	"errors"

	"github.com/opskat/opskat/internal/pkg/jsonfield"
)

// GenericConfig 是通用资产的每资产配置，序列化到 Asset.Config。
//
// CustomType 以自定义类型的标识（slug）引用类型：slug 创建后不可修改
// （Design decision 13），且在导出 / 备份里跨库稳定，而自增 ID 在导入后会变。
//
// Values 以字段名为键。键不存在表示「没有填写」，解析时取字段默认值；键存在
// （即使值为空）表示用户显式填写的值。
type GenericConfig struct {
	CustomType string                  `json:"custom_type"`
	Values     map[string]GenericValue `json:"values,omitempty"`
	GenericConnection
}

// GenericConnection 是 HTTP 执行方式的连接配置，JSON 平铺在 GenericConfig 里（Design
// decision 17：网络路径随实例而定，配在资产上，复用内置类型的代理链 / TLS 配置区；SSH 隧道
// 走 Asset.SSHTunnelID）。命令执行方式忽略它们。拨号出错时原样报错，不会退回直连或跳过
// 校验（connpool.NewHTTPTransport）。
type GenericConnection struct {
	ProxyChain    *ProxyChainConfig `json:"proxy_chain,omitempty"`
	TLSInsecure   bool              `json:"tls_insecure,omitempty"`    // 跳过 TLS 证书校验（仅用户显式开启）
	TLSServerName string            `json:"tls_server_name,omitempty"` // TLS SNI / ServerName
	TLSCAFile     string            `json:"tls_ca_file,omitempty"`     // CA 证书路径
	TLSCertFile   string            `json:"tls_cert_file,omitempty"`   // 客户端证书路径
	TLSKeyFile    string            `json:"tls_key_file,omitempty"`    // 客户端私钥路径
}

// GenericValue 是一个字段的值。存储约定（由 custom_type_svc 维护）：
//   - 非密钥字段：Value 为明文，CredentialID 恒为 0；
//   - 密钥字段：Value 为 credential_svc 加密后的密文，或 CredentialID > 0 引用托管
//     密码凭据（此时 Value 为空）。
type GenericValue struct {
	Value        string `json:"value,omitempty"`
	CredentialID int64  `json:"credential_id,omitempty"`
}

// IsGeneric 判断资产是否为通用资产。
func (a *Asset) IsGeneric() bool { return a.Type == AssetTypeGeneric }

// GetGenericConfig 解析资产配置 JSON 为 GenericConfig。
func (a *Asset) GetGenericConfig() (*GenericConfig, error) {
	if !a.IsGeneric() {
		return nil, errors.New("资产不是通用资产类型")
	}
	return jsonfield.Unmarshal[GenericConfig](a.Config, "通用资产配置")
}

// validateGeneric 校验通用资产引用了自定义类型。类型是否存在、字段值是否符合
// 类型结构依赖数据库，由 custom_type_svc 负责。
func (a *Asset) validateGeneric() error {
	cfg, err := a.GetGenericConfig()
	if err != nil {
		return err
	}
	if cfg.CustomType == "" {
		return errors.New("通用资产必须指定自定义类型")
	}
	return nil
}

// SetGenericConfig 将 cfg 序列化进资产配置 JSON。
func (a *Asset) SetGenericConfig(cfg *GenericConfig) error {
	s, err := jsonfield.Marshal(cfg, "通用资产配置")
	if err != nil {
		return err
	}
	a.Config = s
	return nil
}
