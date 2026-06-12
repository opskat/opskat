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
	WebDAVAutoBackupEnabled         bool   `json:"webdav_auto_backup_enabled,omitempty"`    // WebDAV 自动备份开关
    WebDAVAutoBackupPassword        string `json:"webdav_auto_backup_password,omitempty"`   // 加密后的自动备份密码
    WebDAVAutoBackupLastAt          int64  `json:"webdav_auto_backup_last_at,omitempty"`    // 最近一次自动备份成功时间
    WebDAVAutoBackupLastError       string `json:"webdav_auto_backup_last_error,omitempty"` // 最近一次自动备份错误摘要
	LastUpdateCheck                 int64  `json:"last_update_check,omitempty"` // 上次自动检查更新的 Unix 时间戳
	DebugMode                       bool   `json:"debug_mode,omitempty"`        // 开启后日志级别降为 debug
	WindowWidth                     int    `json:"window_width,omitempty"`      // 上次正常窗口宽度
	WindowHeight                    int    `json:"window_height,omitempty"`     // 上次正常窗口高度

	// 外部编辑配置。仅持久化用户自定义编辑器；内置候选由运行时探测生成。
	ExternalEditDefaultEditorID      string                 `json:"external_edit_default_editor_id,omitempty"`
	ExternalEditWorkspaceRoot        string                 `json:"external_edit_workspace_root,omitempty"`
	ExternalEditCustomEditors        []ExternalEditorConfig `json:"external_edit_custom_editors,omitempty"`
	ExternalEditCleanupRetentionDays int                    `json:"external_edit_cleanup_retention_days,omitempty"`
	ExternalEditMaxReadFileSizeMB    int                    `json:"external_edit_max_read_file_size_mb,omitempty"`
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
	// configMu 串行化对 appConfig 的运行时读写。GetConfig 返回隔离副本、
	// SaveConfig/UpdateConfig 在锁内改写并落盘，避免后台 goroutine（自动备份、
	// 自动更新检查）与前端 IPC 写入并发改同一结构体导致数据竞争或 config.json 写坏。
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

// GetConfig 返回当前配置的隔离副本（须在 LoadConfig 之后调用；未加载时返回 nil）。
//
// 返回副本而非共享指针：调用方可安全读取、临时修改返回值，既不会与后台 goroutine
// 的写入发生数据竞争，也不会意外改动全局状态。要持久化修改，请用 UpdateConfig
// （读-改-存原子化）或 SaveConfig（整体替换）。
func GetConfig() *AppConfig {
	configMu.Lock()
	defer configMu.Unlock()
	if appConfig == nil {
		return nil
	}
	return cloneConfig(appConfig)
}

// SaveConfig 用传入配置整体替换全局配置并持久化。
//
// 适用于在单一来源构造完整配置的场景（如启动初始化）。并发的“读字段-改字段-存”
// 必须改用 UpdateConfig，否则会覆盖其他写入方的并发更新。入参会被深拷贝，调用方
// 之后对其的修改不会影响已保存配置。
func SaveConfig(cfg *AppConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	appConfig = cloneConfig(cfg)
	return saveConfigFile()
}

// UpdateConfig 在锁内对全局配置应用 mutate 并立即落盘，保证并发的读-改-存不丢更新、
// 也不与 GetConfig 的读取竞争。mutate 只应修改传入的 *AppConfig 字段，不要保存其指针。
func UpdateConfig(mutate func(*AppConfig)) error {
	configMu.Lock()
	defer configMu.Unlock()
	if appConfig == nil {
		return errors.New("config not loaded")
	}
	mutate(appConfig)
	return saveConfigFile()
}

func saveConfigFile() error {
	data, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}

// cloneConfig 返回 AppConfig 的隔离副本，深拷贝其中的切片字段，
// 使副本与全局配置不共享任何可变底层数组。
func cloneConfig(c *AppConfig) *AppConfig {
	clone := *c
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
