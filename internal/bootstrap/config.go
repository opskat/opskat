package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// AppConfig 应用持久化配置（config.json）
type AppConfig struct {
	UpdateChannel                   string `json:"update_channel,omitempty"`                    // stable, beta, nightly
	DownloadMirror                  string `json:"download_mirror,omitempty"`                   // 下载镜像 URL 前缀，空表示直连 GitHub
	ExtensionMirror                 string `json:"extension_mirror,omitempty"`                  // 扩展 OCI 拉取源（host[:port][/路径前缀]），空表示直连 ghcr.io
	KDFSalt                         string `json:"kdf_salt,omitempty"`                          // base64 编码的 Argon2id salt
	AIProviderType                  string `json:"ai_provider_type,omitempty"`                  // openai, local_cli
	AIAPIBase                       string `json:"ai_api_base,omitempty"`                       // API base URL 或 CLI 路径
	AIAPIKey                        string `json:"ai_api_key,omitempty"`                        // 加密后的 API Key
	AIModel                         string `json:"ai_model,omitempty"`                          // 模型名或 CLI 类型
	GitHubToken                     string `json:"github_token,omitempty"`                      // 加密后的 GitHub token
	GitHubUser                      string `json:"github_user,omitempty"`                       // GitHub 用户名（非敏感）
	WebDAVURL                       string `json:"webdav_url,omitempty"`                        // WebDAV 备份目录
	WebDAVAuthType                  string `json:"webdav_auth_type,omitempty"`                  // "none" | "basic" | "bearer"
	WebDAVUsername                  string `json:"webdav_username,omitempty"`                   // WebDAV 用户名（非敏感，仅 basic）
	WebDAVPassword                  string `json:"webdav_password,omitempty"`                   // 加密后的 WebDAV 密码（仅 basic）
	WebDAVToken                     string `json:"webdav_token,omitempty"`                      // 加密后的 Bearer token（仅 bearer）
	WebDAVExportDefaultsConfigured  bool   `json:"webdav_export_defaults_configured,omitempty"` // 是否已保存 WebDAV 导出默认项
	WebDAVExportPassword            string `json:"webdav_export_password,omitempty"`            // 加密后的 WebDAV 备份密码
	WebDAVExportIncludeCredentials  bool   `json:"webdav_export_include_credentials,omitempty"`
	WebDAVExportIncludeForwards     bool   `json:"webdav_export_include_forwards,omitempty"`
	WebDAVExportIncludePolicyGroups bool   `json:"webdav_export_include_policy_groups,omitempty"`
	WebDAVExportIncludeShortcuts    bool   `json:"webdav_export_include_shortcuts,omitempty"`
	WebDAVExportIncludeThemes       bool   `json:"webdav_export_include_themes,omitempty"`

	// 自动备份设置（docs/specs/2026-11-17-auto-backup-targets.md）；运行状态在 auto_backup_state.json。
	AutoBackupEnabled         bool     `json:"auto_backup_enabled,omitempty"`
	AutoBackupPassword        string   `json:"auto_backup_password,omitempty"`         // 加密后的自动备份密码
	AutoBackupIntervalMinutes int      `json:"auto_backup_interval_minutes,omitempty"` // 0 = 默认（30）
	AutoBackupKeep            int      `json:"auto_backup_keep,omitempty"`             // 0 = 默认（10）
	AutoBackupTargets         []string `json:"auto_backup_targets,omitempty"`          // 勾选的备份位置 ID
	AutoBackupLocalDir        string   `json:"auto_backup_local_dir,omitempty"`        // 本地文件夹位置的路径

	LastUpdateCheck int64 `json:"last_update_check,omitempty"` // 上次自动检查更新的 Unix 时间戳
	DebugMode       bool  `json:"debug_mode,omitempty"`        // 开启后日志级别降为 debug
	WindowWidth     int   `json:"window_width,omitempty"`      // 上次正常窗口宽度
	WindowHeight    int   `json:"window_height,omitempty"`     // 上次正常窗口高度

	// SSH 空闲保活心跳的全局默认间隔（秒），作用于所有出站 SSH 连接（终端会话 +
	// 连接池）。<=0 = 采用内置默认（30s）。单个资产可在其 SSH 配置里覆盖此值。
	SSHKeepAliveIntervalSeconds int `json:"ssh_keepalive_interval_seconds,omitempty"`

	// 网络代理设置；nil 表示默认（使用环境变量）。
	NetworkProxy *NetworkProxyConfig `json:"network_proxy,omitempty"`

	// 外部编辑配置。仅持久化用户自定义编辑器；内置候选由运行时探测生成。
	ExternalEditDefaultEditorID      string                 `json:"external_edit_default_editor_id,omitempty"`
	ExternalEditWorkspaceRoot        string                 `json:"external_edit_workspace_root,omitempty"`
	ExternalEditCustomEditors        []ExternalEditorConfig `json:"external_edit_custom_editors,omitempty"`
	ExternalEditCleanupRetentionDays int                    `json:"external_edit_cleanup_retention_days,omitempty"`
	ExternalEditMaxReadFileSizeMB    int                    `json:"external_edit_max_read_file_size_mb,omitempty"`

	// 命令的模型审核（辅助审批 / Autopilot）。桌面端和 opsctl 读同一份。
	// CommandReviewTimeoutMs 是每次调用审核扩展的超时，0 为默认值。
	CommandReviewTimeoutMs int `json:"command_review_timeout_ms,omitempty"`
	// CommandReviewExtension 是选用的审核扩展名。空表示启用中的审核扩展只有一个时用它。
	CommandReviewExtension string `json:"command_review_extension,omitempty"`
}

// NetworkProxyConfig 是持久化的网络代理设置。非「自定义」模式下自定义字段也保留，便于切回。
type NetworkProxyConfig struct {
	Mode     string `json:"mode"`               // env | direct | custom
	Type     string `json:"type"`               // http | socks5
	Host     string `json:"host,omitempty"`     // 代理主机
	Port     int    `json:"port,omitempty"`     // 代理端口
	Username string `json:"username,omitempty"` // 代理用户名（非敏感）
	Password string `json:"password,omitempty"` // 加密后的代理密码
	NoProxy  string `json:"no_proxy"`           // 不走代理的地址，每行一项；用户可有意清空，故不 omitempty
}

// ExternalEditorConfig 是用户自定义外部编辑器配置。
type ExternalEditorConfig struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
}

var (
	appConfig     *AppConfig
	appConfigOnce sync.Once
	configPath    string
	// configMu 串行化对 appConfig 的运行时读写：GetConfig 返回隔离副本，
	// SaveConfig / UpdateConfig 在锁内改写并落盘，后台 goroutine 与 IPC 写入不会
	// 并发改同一个结构体。
	configMu sync.Mutex
)

// LoadConfig 加载应用配置，首次调用时自动生成默认值
// 必须在 Init 之后调用（依赖 dataDir）
func LoadConfig(dataDir string) (*AppConfig, error) {
	var loadErr error
	appConfigOnce.Do(func() {
		if dataDir == "" {
			dataDir = AppDataDir()
		}
		configPath = filepath.Join(dataDir, "config.json")

		data, err := os.ReadFile(configPath) //nolint:gosec // path from app data directory
		if err != nil {
			appConfig = &AppConfig{}
			loadErr = saveConfigFile()
			return
		}

		var cfg AppConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			appConfig = &AppConfig{}
			loadErr = saveConfigFile()
			return
		}

		appConfig = &cfg
	})
	return appConfig, loadErr
}

// GetConfig 返回当前配置的隔离副本（LoadConfig 之后调用；未加载时返回 nil）。
// 修改返回值不影响全局配置；要持久化，用 UpdateConfig（读-改-存原子化）或 SaveConfig（整体替换）。
func GetConfig() *AppConfig {
	configMu.Lock()
	defer configMu.Unlock()
	if appConfig == nil {
		return nil
	}
	return cloneConfig(appConfig)
}

// DefaultExtensionRegistryHost 是扩展 OCI 制品的默认（直连）registry 主机。
const DefaultExtensionRegistryHost = "ghcr.io"

// ExtensionRegistry 返回扩展拉取实际使用的 registry：配置的镜像
// （host[:port][/路径前缀]），未配置时为 ghcr.io。
func ExtensionRegistry() string {
	configMu.Lock()
	defer configMu.Unlock()
	if appConfig == nil || appConfig.ExtensionMirror == "" {
		return DefaultExtensionRegistryHost
	}
	return appConfig.ExtensionMirror
}

// SaveConfig 用传入配置整体替换全局配置并持久化；入参被深拷贝，之后修改它不影响已保存配置。
// 与其他写入方并发的「读字段-改字段-存」应改用 UpdateConfig，否则可能覆盖对方的更新。
func SaveConfig(cfg *AppConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	appConfig = cloneConfig(cfg)
	return saveConfigFile()
}

// UpdateConfig 在锁内对全局配置应用 mutate 并落盘：并发的读-改-存不丢更新。
// mutate 只应修改传入的 *AppConfig，不要保存它的指针。
func UpdateConfig(mutate func(*AppConfig)) error {
	configMu.Lock()
	defer configMu.Unlock()
	if appConfig == nil {
		return errors.New("config not loaded")
	}
	mutate(appConfig)
	return saveConfigFile()
}

// cloneConfig 深拷贝 AppConfig，副本与原值不共享任何可变的切片 / 指针。
func cloneConfig(c *AppConfig) *AppConfig {
	clone := *c
	if c.NetworkProxy != nil {
		proxy := *c.NetworkProxy
		clone.NetworkProxy = &proxy
	}
	if c.AutoBackupTargets != nil {
		clone.AutoBackupTargets = append([]string(nil), c.AutoBackupTargets...)
	}
	if c.ExternalEditCustomEditors != nil {
		editors := make([]ExternalEditorConfig, len(c.ExternalEditCustomEditors))
		copy(editors, c.ExternalEditCustomEditors)
		for i := range editors {
			if src := c.ExternalEditCustomEditors[i].Args; src != nil {
				editors[i].Args = append([]string(nil), src...)
			}
		}
		clone.ExternalEditCustomEditors = editors
	}
	return &clone
}

func saveConfigFile() error {
	data, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}
