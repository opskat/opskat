package asset_entity

import (
	"encoding/json"
	"errors"
	"fmt"
)

// 资产类型常量
const (
	AssetTypeSSH = "ssh"
)

// 认证方式常量
const (
	AuthTypePassword = "password"
	AuthTypeKey      = "key"
)

// 状态常量
const (
	StatusActive  = 1
	StatusDeleted = 2
)

// Asset 通用资产实体（充血模型）
type Asset struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name        string `gorm:"column:name;type:varchar(255);not null"`
	Type        string `gorm:"column:type;type:varchar(50);not null;index"`
	GroupID     int64  `gorm:"column:group_id;index"`
	Icon        string `gorm:"column:icon;type:varchar(100)"`
	Tags        string `gorm:"column:tags;type:text"`
	Description string `gorm:"column:description;type:text"`
	Config      string `gorm:"column:config;type:text"`
	SortOrder   int    `gorm:"column:sort_order;default:0"`
	Status      int    `gorm:"column:status;default:1"`
	Createtime  int64  `gorm:"column:createtime"`
	Updatetime  int64  `gorm:"column:updatetime"`
}

// TableName GORM表名
func (Asset) TableName() string {
	return "assets"
}

// SSHConfig SSH类型的特定配置
type SSHConfig struct {
	Host           string          `json:"host"`
	Port           int             `json:"port"`
	Username       string          `json:"username"`
	AuthType       string          `json:"auth_type"`
	Password       string          `json:"password,omitempty"`       // 加密后的密码
	KeyID          int64           `json:"key_id,omitempty"`         // 托管密钥 ID
	KeySource      string          `json:"key_source,omitempty"`     // "managed" | "file"
	PrivateKeys    []string        `json:"private_keys,omitempty"`
	JumpHostID     int64           `json:"jump_host_id,omitempty"`
	ForwardedPorts []ForwardedPort `json:"forwarded_ports,omitempty"`
	Proxy          *ProxyConfig    `json:"proxy,omitempty"`
	LastConnected  int64           `json:"last_connected,omitempty"`
}

// ForwardedPort 端口转发配置
type ForwardedPort struct {
	Type       string `json:"type"`        // "local" | "remote" | "dynamic"
	LocalHost  string `json:"local_host"`
	LocalPort  int    `json:"local_port"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
}

// ProxyConfig 代理配置
type ProxyConfig struct {
	Type     string `json:"type"`                  // "socks5" | "socks4" | "http"
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// --- 充血模型方法 ---

// IsSSH 判断是否SSH类型
func (a *Asset) IsSSH() bool {
	return a.Type == AssetTypeSSH
}

// GetSSHConfig 解析SSH配置
func (a *Asset) GetSSHConfig() (*SSHConfig, error) {
	if !a.IsSSH() {
		return nil, errors.New("资产不是SSH类型")
	}
	if a.Config == "" {
		return nil, errors.New("SSH配置为空")
	}
	var cfg SSHConfig
	if err := json.Unmarshal([]byte(a.Config), &cfg); err != nil {
		return nil, fmt.Errorf("解析SSH配置失败: %w", err)
	}
	return &cfg, nil
}

// SetSSHConfig 序列化SSH配置到Config字段
func (a *Asset) SetSSHConfig(cfg *SSHConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化SSH配置失败: %w", err)
	}
	a.Config = string(data)
	return nil
}

// Validate 校验资产必填字段和类型配置的完整性
func (a *Asset) Validate() error {
	if a.Name == "" {
		return errors.New("资产名称不能为空")
	}
	if a.Type == "" {
		return errors.New("资产类型不能为空")
	}

	// 校验类型是否合法
	switch a.Type {
	case AssetTypeSSH:
		return a.validateSSH()
	default:
		return fmt.Errorf("无效的资产类型: %s", a.Type)
	}
}

// validateSSH 校验SSH类型特定配置
func (a *Asset) validateSSH() error {
	cfg, err := a.GetSSHConfig()
	if err != nil {
		return fmt.Errorf("SSH配置无效: %w", err)
	}
	if cfg.Host == "" {
		return errors.New("SSH主机地址不能为空")
	}
	if cfg.Port <= 0 {
		return errors.New("SSH端口无效")
	}
	if cfg.Username == "" {
		return errors.New("SSH用户名不能为空")
	}
	if cfg.AuthType == "" {
		return errors.New("SSH认证方式不能为空")
	}
	return nil
}

// CanConnect 判断资产是否处于可连接状态
func (a *Asset) CanConnect() bool {
	if a.Status != StatusActive {
		return false
	}
	if !a.IsSSH() {
		return false
	}
	cfg, err := a.GetSSHConfig()
	if err != nil {
		return false
	}
	return cfg.Host != "" && cfg.Port > 0
}

// SSHAddress 返回 host:port 格式地址
func (a *Asset) SSHAddress() (string, error) {
	cfg, err := a.GetSSHConfig()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), nil
}
