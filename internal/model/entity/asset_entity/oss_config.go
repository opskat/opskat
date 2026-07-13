package asset_entity

import (
	"errors"

	"github.com/opskat/opskat/internal/pkg/jsonfield"
)

// OSSConfig 是对象存储(OSS)资产的每资产配置,序列化到 Asset.Config。
type OSSConfig struct {
	Provider        string            `json:"provider"` // UI provider preset; connection behavior is determined by endpoint/region/addressing fields
	Endpoint        string            `json:"endpoint"` // host 或 scheme://host[:port]
	Region          string            `json:"region"`
	AccessKeyID     string            `json:"access_key_id"`
	SecretAccessKey string            `json:"secret_access_key"` // 内联时为 AES-256-GCM 密文;托管时为空
	CredentialID    int64             `json:"credential_id"`     // >0 表示引用托管密码凭证
	UsePathStyle    bool              `json:"use_path_style"`
	UseSSL          bool              `json:"use_ssl"`
	SkipTLSVerify   bool              `json:"skip_tls_verify"`
	ConnectTimeout  int               `json:"connect_timeout"` // 秒;0 表示默认
	PartSizeMB      int               `json:"part_size_mb"`    // 0 表示由 SDK 自动选择
	ProxyChain      *ProxyChainConfig `json:"proxy_chain,omitempty"`
}

// GetCredentialID 实现 credential_resolver.PasswordSource。
func (c *OSSConfig) GetCredentialID() int64 { return c.CredentialID }

// GetPassword 实现 credential_resolver.PasswordSource。
func (c *OSSConfig) GetPassword() string { return c.SecretAccessKey }

// IsOSS 判断资产是否为对象存储类型。
func (a *Asset) IsOSS() bool { return a.Type == AssetTypeOSS }

// GetOSSConfig 解析资产配置 JSON 为 OSSConfig。
func (a *Asset) GetOSSConfig() (*OSSConfig, error) {
	if !a.IsOSS() {
		return nil, errors.New("资产不是OSS类型")
	}
	return jsonfield.Unmarshal[OSSConfig](a.Config, "OSS配置")
}

// SetOSSConfig 将 cfg 序列化进资产配置 JSON。
func (a *Asset) SetOSSConfig(cfg *OSSConfig) error {
	s, err := jsonfield.Marshal(cfg, "OSS配置")
	if err != nil {
		return err
	}
	a.Config = s
	return nil
}
