package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/buildinfo"
	"github.com/opskat/opskat/internal/embedded"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/pkg/executil"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/service/backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/import_svc"
	"github.com/opskat/opskat/internal/service/update_svc"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// --- GitHub Token ---

// SaveGitHubToken 加密保存 GitHub token
func (s *System) SaveGitHubToken(token, user string) error {
	cfg := bootstrap.GetConfig()
	if token == "" {
		cfg.GitHubToken = ""
		cfg.GitHubUser = ""
	} else {
		encrypted, err := credential_svc.Default().Encrypt(token)
		if err != nil {
			return fmt.Errorf("加密 GitHub Token 失败: %w", err)
		}
		cfg.GitHubToken = encrypted
		cfg.GitHubUser = user
	}
	return bootstrap.SaveConfig(cfg)
}

// GetGitHubToken 获取解密后的 GitHub token
func (s *System) GetGitHubToken() (string, error) {
	cfg := bootstrap.GetConfig()
	if cfg.GitHubToken == "" {
		return "", nil
	}
	return credential_svc.Default().Decrypt(cfg.GitHubToken)
}

// GetStoredGitHubUser 获取保存的 GitHub 用户名
func (s *System) GetStoredGitHubUser() string {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return ""
	}
	return cfg.GitHubUser
}

// ClearGitHubToken 清除保存的 GitHub token
func (s *System) ClearGitHubToken() error {
	return s.SaveGitHubToken("", "")
}

// --- 导入导出 ---

// PreviewTabbyConfig 预览 Tabby 配置（不写入数据库）
// 自动检测默认路径，找不到则弹出文件选择框
func (s *System) PreviewTabbyConfig() (*import_svc.PreviewResult, error) {
	data, err := s.readTabbyConfig()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	return import_svc.PreviewTabbyConfig(i18n.Ctx(s.ctx, s.Lang()), data)
}

// ImportTabbySelected 导入用户选中的 Tabby 连接
func (s *System) ImportTabbySelected(selectedIndexes []int, passphrase string, overwrite bool) (*import_svc.ImportResult, error) {
	data, err := s.readTabbyConfig()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	return import_svc.ImportTabbySelected(i18n.Ctx(s.ctx, s.Lang()), data, selectedIndexes, import_svc.ImportOptions{
		Passphrase: passphrase,
		Overwrite:  overwrite,
	})
}

// PreviewSSHConfig 预览 SSH Config 文件（不写入数据库）
// 自动检测 ~/.ssh/config，找不到则弹出文件选择框
func (s *System) PreviewSSHConfig() (*import_svc.PreviewResult, error) {
	data, err := s.readSSHConfig()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	return import_svc.PreviewSSHConfig(i18n.Ctx(s.ctx, s.Lang()), data)
}

// ImportSSHConfigSelected 导入用户选中的 SSH Config 连接
func (s *System) ImportSSHConfigSelected(selectedIndexes []int, overwrite bool) (*import_svc.ImportResult, error) {
	data, err := s.readSSHConfig()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	return import_svc.ImportSSHConfigSelected(i18n.Ctx(s.ctx, s.Lang()), data, selectedIndexes, import_svc.ImportOptions{
		Overwrite: overwrite,
	})
}

// PreviewWindTermConfig 预览 WindTerm 配置（不写入数据库）
// 用户自行选择 WindTerm profile 下的 terminal/user.sessions 文件
func (s *System) PreviewWindTermConfig() (*import_svc.WindTermPreviewResult, error) {
	data, err := s.readWindTermConfig()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	preview, err := import_svc.PreviewWindTermConfig(i18n.Ctx(s.ctx, s.Lang()), data)
	if err != nil {
		return nil, err
	}
	sourceID, err := import_svc.NewWindTermImportSession(data)
	if err != nil {
		return nil, err
	}
	return &import_svc.WindTermPreviewResult{Preview: preview, SourceID: sourceID}, nil
}

// ImportWindTermSelected 导入用户选中的 WindTerm 连接
func (s *System) ImportWindTermSelected(sourceID string, selectedIndexes []int, overwrite bool) (*import_svc.ImportResult, error) {
	data, ok := import_svc.WindTermImportSessionData(sourceID)
	if !ok {
		return nil, fmt.Errorf("请先选择 WindTerm user.sessions 文件并完成预览")
	}
	result, err := import_svc.ImportWindTermSelected(i18n.Ctx(s.ctx, s.Lang()), data, selectedIndexes, import_svc.ImportOptions{
		Overwrite: overwrite,
	})
	if err == nil {
		import_svc.DeleteWindTermImportSession(sourceID)
	}
	return result, err
}

// readSSHConfig 读取 SSH Config 文件
func (s *System) readSSHConfig() ([]byte, error) {
	filePath := import_svc.DetectSSHConfigPath()
	if filePath == "" {
		var err error
		filePath, err = wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
			Title: "选择 SSH Config 文件",
			Filters: []wailsRuntime.FileFilter{
				{DisplayName: "All Files", Pattern: "*"},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("打开文件对话框失败: %w", err)
		}
		if filePath == "" {
			return nil, nil
		}
	}
	data, err := os.ReadFile(filePath) //nolint:gosec // filePath is from file dialog or known config path
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return data, nil
}

// readWindTermConfig 读取 WindTerm user.sessions 文件内容
func (s *System) readWindTermConfig() ([]byte, error) {
	filePath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 WindTerm 配置文件：profiles/default.v10/terminal/user.sessions",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "WindTerm user.sessions", Pattern: "user.sessions"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filePath) //nolint:gosec // filePath is from file dialog
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return data, nil
}

// readTabbyConfig 读取 Tabby 配置文件内容
func (s *System) readTabbyConfig() ([]byte, error) {
	filePath := detectTabbyConfigPath()
	if filePath == "" {
		var err error
		filePath, err = wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
			Title: "选择 Tabby 配置文件",
			Filters: []wailsRuntime.FileFilter{
				{DisplayName: "YAML Files", Pattern: "*.yaml;*.yml"},
				{DisplayName: "All Files", Pattern: "*"},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("打开文件对话框失败: %w", err)
		}
		if filePath == "" {
			return nil, nil
		}
	}
	data, err := os.ReadFile(filePath) //nolint:gosec // filePath is from file dialog or known config path
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return data, nil
}

// detectTabbyConfigPath 检测 Tabby 配置文件默认路径
func detectTabbyConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			filepath.Join(homeDir, "Library", "Application Support", "tabby", "config.yaml"),
		}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			candidates = []string{
				filepath.Join(appData, "Tabby", "config.yaml"),
			}
		}
	case "linux":
		candidates = []string{
			filepath.Join(homeDir, ".config", "tabby", "config.yaml"),
		}
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil { //nolint:gosec // path is from known config locations
			return path
		}
	}
	return ""
}

// ExportData 导出所有资产和分组为 JSON（剪贴板用，不含凭据）
func (s *System) ExportData() (string, error) {
	opts := &backup_svc.ExportOptions{}
	data, err := backup_svc.Export(i18n.Ctx(s.ctx, s.Lang()), opts, nil)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// --- 备份操作 ---

// ExportToFile 导出备份到文件
func (s *System) ExportToFile(password string, opts backup_svc.ExportOptions) error {
	if opts.IncludeCredentials && password == "" {
		return fmt.Errorf("包含凭据时必须设置备份密码")
	}

	var crypto backup_svc.CredentialCrypto
	if opts.IncludeCredentials {
		crypto = credential_svc.Default()
	}

	data, err := backup_svc.Export(i18n.Ctx(s.ctx, s.Lang()), &opts, crypto)
	if err != nil {
		return err
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	var output []byte
	var defaultName string
	if password != "" {
		output, err = backup_svc.EncryptBackup(jsonData, password)
		if err != nil {
			return err
		}
		defaultName = fmt.Sprintf("opskat-backup-%s.encrypted.json", time.Now().Format("20060102"))
	} else {
		output = jsonData
		defaultName = fmt.Sprintf("opskat-backup-%s.json", time.Now().Format("20060102"))
	}

	filePath, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON Files", Pattern: "*.json"},
		},
	})
	if err != nil {
		return fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil
	}

	return os.WriteFile(filePath, output, 0644)
}

// ImportFileInfo 导入文件信息
type ImportFileInfo struct {
	FilePath  string                    `json:"filePath"`
	Encrypted bool                      `json:"encrypted"`
	Summary   *backup_svc.BackupSummary `json:"summary,omitempty"`
}

// SelectImportFile 选择备份文件并检测是否加密，返回概览信息
func (s *System) SelectImportFile() (*ImportFileInfo, error) {
	filePath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "导入备份",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON Files", Pattern: "*.json"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil, nil
	}

	fileData, err := os.ReadFile(filePath) //nolint:gosec // filePath is from file dialog
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	info := &ImportFileInfo{
		FilePath:  filePath,
		Encrypted: backup_svc.IsEncryptedBackup(fileData),
	}
	// 非加密备份可直接解析概览
	if !info.Encrypted {
		var data backup_svc.BackupData
		if err := json.Unmarshal(fileData, &data); err == nil {
			info.Summary = data.Summary()
		}
	}
	return info, nil
}

// PreviewImportFile 解密并预览备份文件概览
func (s *System) PreviewImportFile(filePath, password string) (*backup_svc.BackupSummary, error) {
	fileData, err := os.ReadFile(filePath) //nolint:gosec // filePath is from previous file dialog
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	var jsonData []byte
	if backup_svc.IsEncryptedBackup(fileData) {
		jsonData, err = backup_svc.DecryptBackup(fileData, password)
		if err != nil {
			return nil, err
		}
	} else {
		jsonData = fileData
	}

	var data backup_svc.BackupData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("解析备份数据失败: %w", err)
	}
	summary := data.Summary()
	summary.Encrypted = backup_svc.IsEncryptedBackup(fileData)
	return summary, nil
}

// ExecuteImportFile 执行文件导入
func (s *System) ExecuteImportFile(filePath, password string, opts backup_svc.ImportOptions) (*backup_svc.ImportResult, error) {
	fileData, err := os.ReadFile(filePath) //nolint:gosec // filePath is from previous file dialog selection
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	var jsonData []byte
	if backup_svc.IsEncryptedBackup(fileData) {
		jsonData, err = backup_svc.DecryptBackup(fileData, password)
		if err != nil {
			return nil, err
		}
	} else {
		jsonData = fileData
	}

	var data backup_svc.BackupData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("解析备份数据失败: %w", err)
	}

	return backup_svc.Import(i18n.Ctx(s.ctx, s.Lang()), &data, &opts, credential_svc.Default())
}

// --- GitHub 认证 ---

// StartGitHubDeviceFlow 发起 GitHub Device Flow 认证
func (s *System) StartGitHubDeviceFlow() (*backup_svc.DeviceFlowInfo, error) {
	return backup_svc.StartDeviceFlow()
}

// WaitGitHubDeviceAuth 等待用户完成 GitHub 授权，返回 access_token
func (s *System) WaitGitHubDeviceAuth(deviceCode string, interval int) (string, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
	s.githubAuthCancel = cancel
	defer func() {
		cancel()
		s.githubAuthCancel = nil
	}()
	return backup_svc.PollDeviceAuth(ctx, deviceCode, interval)
}

// CancelGitHubAuth 取消 GitHub 授权等待
func (s *System) CancelGitHubAuth() {
	if s.githubAuthCancel != nil {
		s.githubAuthCancel()
	}
}

// GetGitHubUser 获取 GitHub 用户信息
func (s *System) GetGitHubUser(token string) (*backup_svc.GitHubUser, error) {
	return backup_svc.GetGitHubUser(token)
}

// WebDAVStoredConfig 是前端可读取的 WebDAV 配置；password / token 解密后明文回填，
// 便于设置页编辑时直接显示已有值（数据未离开本地进程，加密存储已在落盘层做）。
type WebDAVStoredConfig struct {
	URL                       string `json:"url"`
	AuthType                  string `json:"authType"`
	Username                  string `json:"username,omitempty"`
	Password                  string `json:"password,omitempty"`
	Token                     string `json:"token,omitempty"`
	Configured                bool   `json:"configured"`
	ExportDefaultsConfigured  bool   `json:"exportDefaultsConfigured"`
	ExportPassword            string `json:"exportPassword,omitempty"`
	ExportIncludeCredentials  bool   `json:"exportIncludeCredentials"`
	ExportIncludeForwards     bool   `json:"exportIncludeForwards"`
	ExportIncludePolicyGroups bool   `json:"exportIncludePolicyGroups"`
	ExportIncludeShortcuts    bool   `json:"exportIncludeShortcuts"`
	ExportIncludeThemes       bool   `json:"exportIncludeThemes"`
}

// WebDAVSaveInput 是 SaveWebDAVConfig / TestWebDAVConfig 的入参，把鉴权方式与凭据收成一个 struct。
type WebDAVSaveInput struct {
	URL      string `json:"url"`
	AuthType string `json:"authType"`
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

// toServiceConfig 将入参转换为 backup_svc.WebDAVConfig，并在 AuthType 为空时兜底为 WebDAVAuthNone。
func (in WebDAVSaveInput) toServiceConfig() backup_svc.WebDAVConfig {
	cfg := backup_svc.WebDAVConfig{
		URL:      strings.TrimSpace(in.URL),
		AuthType: backup_svc.WebDAVAuthType(in.AuthType),
		Username: strings.TrimSpace(in.Username),
		Password: in.Password,
		Token:    strings.TrimSpace(in.Token),
	}
	if cfg.AuthType == "" {
		cfg.AuthType = backup_svc.WebDAVAuthNone
	}
	return cfg
}

// --- Gist 备份 ---

// ExportToGist 加密并上传备份到 Gist
func (s *System) ExportToGist(password, token, gistID string, opts backup_svc.ExportOptions) (*backup_svc.GistInfo, error) {
	var crypto backup_svc.CredentialCrypto
	if opts.IncludeCredentials {
		crypto = credential_svc.Default()
	}

	data, err := backup_svc.Export(i18n.Ctx(s.ctx, s.Lang()), &opts, crypto)
	if err != nil {
		return nil, err
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	encrypted, err := backup_svc.EncryptBackup(jsonData, password)
	if err != nil {
		return nil, err
	}

	return backup_svc.CreateOrUpdateGist(token, gistID, encrypted)
}

// ListBackupGists 列出用户的备份 Gist
func (s *System) ListBackupGists(token string) ([]*backup_svc.GistInfo, error) {
	return backup_svc.ListBackupGists(token)
}

// PreviewGistBackup 预览 Gist 备份概览
func (s *System) PreviewGistBackup(gistID, password, token string) (*backup_svc.BackupSummary, error) {
	content, err := backup_svc.GetGistContent(token, gistID)
	if err != nil {
		return nil, err
	}

	jsonData, err := backup_svc.DecryptBackup(content, password)
	if err != nil {
		return nil, err
	}

	var data backup_svc.BackupData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("解析备份数据失败: %w", err)
	}
	summary := data.Summary()
	summary.Encrypted = true
	return summary, nil
}

// ImportFromGist 从 Gist 导入备份
func (s *System) ImportFromGist(gistID, password, token string, opts backup_svc.ImportOptions) (*backup_svc.ImportResult, error) {
	content, err := backup_svc.GetGistContent(token, gistID)
	if err != nil {
		return nil, err
	}

	jsonData, err := backup_svc.DecryptBackup(content, password)
	if err != nil {
		return nil, err
	}

	var data backup_svc.BackupData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("解析备份数据失败: %w", err)
	}

	return backup_svc.Import(i18n.Ctx(s.ctx, s.Lang()), &data, &opts, credential_svc.Default())
}

// --- WebDAV 备份 ---

// SaveWebDAVConfig 保存 WebDAV 备份配置。按 AuthType 持久化对应字段，并清空其他类型字段以避免历史秘密残留。
func (s *System) SaveWebDAVConfig(in WebDAVSaveInput) error {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	svcCfg := in.toServiceConfig()
	if err := backup_svc.ValidateWebDAVConfig(svcCfg); err != nil {
		return err
	}

	cfg.WebDAVURL = svcCfg.URL
	cfg.WebDAVAuthType = string(svcCfg.AuthType)

	// 清空所有 type 字段，再按当前 type 写回。避免切换鉴权方式后旧凭据仍留在 config.json。
	cfg.WebDAVUsername = ""
	cfg.WebDAVPassword = ""
	cfg.WebDAVToken = ""

	switch svcCfg.AuthType {
	case backup_svc.WebDAVAuthBasic:
		cfg.WebDAVUsername = svcCfg.Username
		encrypted, err := credential_svc.Default().Encrypt(svcCfg.Password)
		if err != nil {
			return fmt.Errorf("加密 WebDAV 密码失败: %w", err)
		}
		cfg.WebDAVPassword = encrypted
	case backup_svc.WebDAVAuthBearer:
		encrypted, err := credential_svc.Default().Encrypt(svcCfg.Token)
		if err != nil {
			return fmt.Errorf("加密 WebDAV token 失败: %w", err)
		}
		cfg.WebDAVToken = encrypted
	}
	return bootstrap.SaveConfig(cfg)
}

// GetWebDAVConfig 读取已保存的 WebDAV 配置，password / token 解密后明文回填。
func (s *System) GetWebDAVConfig() (*WebDAVStoredConfig, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return &WebDAVStoredConfig{}, nil
	}

	authType := cfg.WebDAVAuthType
	if authType == "" && strings.TrimSpace(cfg.WebDAVURL) != "" {
		authType = string(backup_svc.WebDAVAuthNone)
	}

	out := &WebDAVStoredConfig{
		URL:                       cfg.WebDAVURL,
		AuthType:                  authType,
		Username:                  cfg.WebDAVUsername,
		Configured:                strings.TrimSpace(cfg.WebDAVURL) != "",
		ExportDefaultsConfigured:  cfg.WebDAVExportDefaultsConfigured,
		ExportIncludeCredentials:  cfg.WebDAVExportIncludeCredentials,
		ExportIncludeForwards:     cfg.WebDAVExportIncludeForwards,
		ExportIncludePolicyGroups: cfg.WebDAVExportIncludePolicyGroups,
		ExportIncludeShortcuts:    cfg.WebDAVExportIncludeShortcuts,
		ExportIncludeThemes:       cfg.WebDAVExportIncludeThemes,
	}
	if cfg.WebDAVPassword != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVPassword)
		if err != nil {
			return nil, fmt.Errorf("解密 WebDAV 密码失败: %w", err)
		}
		out.Password = decrypted
	}
	if cfg.WebDAVToken != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVToken)
		if err != nil {
			return nil, fmt.Errorf("解密 WebDAV token 失败: %w", err)
		}
		out.Token = decrypted
	}
	if cfg.WebDAVExportPassword != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVExportPassword)
		if err != nil {
			return nil, fmt.Errorf("解密 WebDAV 备份密码失败: %w", err)
		}
		out.ExportPassword = decrypted
	}
	return out, nil
}

// ClearWebDAVConfig 清除 WebDAV 备份配置。
func (s *System) ClearWebDAVConfig() error {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	cfg.WebDAVURL = ""
	cfg.WebDAVAuthType = ""
	cfg.WebDAVUsername = ""
	cfg.WebDAVPassword = ""
	cfg.WebDAVToken = ""
	clearWebDAVExportDefaults(cfg)
	return bootstrap.SaveConfig(cfg)
}

// TestWebDAVConfig 用入参里的字段测试 WebDAV 目录连通性与写权限。
// 完全使用入参字段，不再回退到已存配置——前端已回填明文凭据。
func (s *System) TestWebDAVConfig(in WebDAVSaveInput) error {
	svcCfg := in.toServiceConfig()
	if err := backup_svc.ValidateWebDAVConfig(svcCfg); err != nil {
		return err
	}
	return backup_svc.TestWebDAVConnection(svcCfg)
}

// ListWebDAVBackups 列出 WebDAV 目录中的 OpsKat 备份。
func (s *System) ListWebDAVBackups() ([]*backup_svc.WebDAVBackupInfo, error) {
	cfg, err := s.webDAVConfigFromStorage()
	if err != nil {
		return nil, err
	}
	return backup_svc.ListWebDAVBackups(cfg)
}

// ExportToWebDAV 加密并上传备份到 WebDAV。
func (s *System) ExportToWebDAV(password string, opts backup_svc.ExportOptions) (*backup_svc.WebDAVBackupInfo, error) {
	if password == "" {
		return nil, fmt.Errorf("WebDAV 备份必须设置备份密码")
	}
	cfg, err := s.webDAVConfigFromStorage()
	if err != nil {
		return nil, err
	}

	var crypto backup_svc.CredentialCrypto
	if opts.IncludeCredentials {
		crypto = credential_svc.Default()
	}

	data, err := backup_svc.Export(i18n.Ctx(s.ctx, s.Lang()), &opts, crypto)
	if err != nil {
		return nil, err
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	encrypted, err := backup_svc.EncryptBackup(jsonData, password)
	if err != nil {
		return nil, err
	}

	info, err := backup_svc.CreateOrUpdateWebDAVBackup(cfg, encrypted)
	if err != nil {
		return nil, err
	}
	// 备份已上传成功；记住导出默认项只是次要诉求。即便写 config.json 失败也不能
	// 把"上传成功"误报为失败，否则前端既弹出错误、又不会刷新到刚上传的备份。
	if err := s.saveWebDAVExportDefaults(password, opts); err != nil {
		logger.Default().Warn("save WebDAV export defaults failed", zap.Error(err))
	}
	return info, nil
}

func (s *System) saveWebDAVExportDefaults(password string, opts backup_svc.ExportOptions) error {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	encrypted, err := credential_svc.Default().Encrypt(password)
	if err != nil {
		return fmt.Errorf("加密 WebDAV 备份密码失败: %w", err)
	}
	cfg.WebDAVExportDefaultsConfigured = true
	cfg.WebDAVExportPassword = encrypted
	cfg.WebDAVExportIncludeCredentials = opts.IncludeCredentials
	cfg.WebDAVExportIncludeForwards = opts.IncludeForwards
	cfg.WebDAVExportIncludePolicyGroups = opts.IncludePolicyGroups
	cfg.WebDAVExportIncludeShortcuts = opts.IncludeShortcuts
	cfg.WebDAVExportIncludeThemes = opts.IncludeThemes
	return bootstrap.SaveConfig(cfg)
}

func clearWebDAVExportDefaults(cfg *bootstrap.AppConfig) {
	cfg.WebDAVExportDefaultsConfigured = false
	cfg.WebDAVExportPassword = ""
	cfg.WebDAVExportIncludeCredentials = false
	cfg.WebDAVExportIncludeForwards = false
	cfg.WebDAVExportIncludePolicyGroups = false
	cfg.WebDAVExportIncludeShortcuts = false
	cfg.WebDAVExportIncludeThemes = false
}

// ImportFromWebDAV 从 WebDAV 导入备份。
func (s *System) ImportFromWebDAV(name, password string, opts backup_svc.ImportOptions) (*backup_svc.ImportResult, error) {
	cfg, err := s.webDAVConfigFromStorage()
	if err != nil {
		return nil, err
	}
	content, err := backup_svc.GetWebDAVBackupContent(cfg, name)
	if err != nil {
		return nil, err
	}

	jsonData, err := backup_svc.DecryptBackup(content, password)
	if err != nil {
		return nil, err
	}

	var data backup_svc.BackupData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("解析备份数据失败: %w", err)
	}

	return backup_svc.Import(i18n.Ctx(s.ctx, s.Lang()), &data, &opts, credential_svc.Default())
}

func (s *System) webDAVConfigFromStorage() (backup_svc.WebDAVConfig, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil || strings.TrimSpace(cfg.WebDAVURL) == "" {
		return backup_svc.WebDAVConfig{}, fmt.Errorf("WebDAV 未配置")
	}

	authType := backup_svc.WebDAVAuthType(cfg.WebDAVAuthType)
	if authType == "" {
		authType = backup_svc.WebDAVAuthNone
	}

	out := backup_svc.WebDAVConfig{
		URL:      cfg.WebDAVURL,
		AuthType: authType,
		Username: cfg.WebDAVUsername,
	}
	if cfg.WebDAVPassword != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVPassword)
		if err != nil {
			return backup_svc.WebDAVConfig{}, fmt.Errorf("解密 WebDAV 密码失败: %w", err)
		}
		out.Password = decrypted
	}
	if cfg.WebDAVToken != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVToken)
		if err != nil {
			return backup_svc.WebDAVConfig{}, fmt.Errorf("解密 WebDAV token 失败: %w", err)
		}
		out.Token = decrypted
	}
	return out, nil
}

// GetDataDir 返回应用数据目录
func (s *System) GetDataDir() string {
	return bootstrap.AppDataDir()
}

// OpenDirectory 在系统文件管理器中打开指定目录
func (s *System) OpenDirectory(path string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{path}
	case "windows":
		cmd = "explorer"
		args = []string{path}
	default: // linux
		cmd = "xdg-open"
		args = []string{path}
	}
	c := exec.Command(cmd, args...) //nolint:gosec
	return c.Start()
}

// --- Opsctl 安装 ---

// OpsctlInfo opsctl CLI 检测结果
type OpsctlInfo struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	Embedded  bool   `json:"embedded"` // 桌面端是否内嵌了 opsctl 二进制
}

// DetectOpsctl 检测 opsctl CLI 是否已安装
func (s *System) DetectOpsctl() OpsctlInfo {
	info := OpsctlInfo{
		Embedded: embedded.HasEmbeddedOpsctl(),
	}
	opsctlPath, err := exec.LookPath("opsctl")
	if err != nil {
		// LookPath 用的是进程启动时的 PATH，安装后当前进程感知不到
		// 直接检查默认安装路径
		binName := "opsctl"
		if runtime.GOOS == "windows" {
			binName = "opsctl.exe"
		}
		candidate := filepath.Join(embedded.DefaultInstallDir(), binName)
		if _, statErr := os.Stat(candidate); statErr != nil {
			return info
		}
		opsctlPath = candidate
	}
	info.Installed = true
	info.Path = opsctlPath
	versionCmd := exec.Command(opsctlPath, "version") //nolint:gosec
	executil.HideWindow(versionCmd)
	out, err := versionCmd.Output()
	if err == nil {
		info.Version = strings.TrimSpace(string(out))
	}
	return info
}

// GetOpsctlInstallDir 返回默认安装目录
func (s *System) GetOpsctlInstallDir() string {
	return embedded.DefaultInstallDir()
}

// InstallOpsctl 将内嵌的 opsctl 二进制安装到指定目录
func (s *System) InstallOpsctl(targetDir string) (string, error) {
	if targetDir == "" {
		targetDir = embedded.DefaultInstallDir()
	}
	return embedded.InstallOpsctl(targetDir)
}

// --- Skills / Plugin ---

// SkillTarget AI Skill 安装目标
type SkillTarget struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
}

// skillInstallType 安装格式类型
type skillInstallType int

const (
	installClaude skillInstallType = iota // Claude Code 插件格式
	installSkill                          // 普通 SKILL.md 格式
)

// skillTargetDef 支持的 Skill 安装目标，添加新 CLI 只需在此追加
type skillTargetDef struct {
	Key      string                   // 稳定标识，供前端逐项卸载调用
	Name     string                   // 显示名称
	Type     skillInstallType         // 安装格式
	SkillFn  func(home string) string // 返回安装目录
	DetectFn func(path string) bool   // 检测是否已安装
}

// SkillAgent 通用目录（~/.agents/skills）受益的 agent
type SkillAgent struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// SkillInstallInfo Skill 安装状态总览
type SkillInstallInfo struct {
	UniversalPath      string        `json:"universalPath"`
	UniversalInstalled bool          `json:"universalInstalled"`
	UniversalAgents    []SkillAgent  `json:"universalAgents"`
	Standalone         []SkillTarget `json:"standalone"`
}

const universalSkillKey = "universal"

// universalSkillDir 返回跨 agent 共享目录 ~/.agents/skills/opsctl
// 该目录遵循 Agent Skills 通用约定，被 Pi/Codex/OpenCode/Cursor/Copilot/Windsurf/Gemini CLI/Cline/Warp 等读取。
func universalSkillDir(home string) string {
	return filepath.Join(home, ".agents", "skills", pluginName)
}

// universalAgents 支持读取 ~/.agents/skills 的 agent，一次安装全部生效
var universalAgents = []SkillAgent{
	{"pi", "Pi"},
	{"codex", "Codex"},
	{"opencode", "OpenCode"},
	{"cursor", "Cursor"},
	{"copilot", "GitHub Copilot"},
	{"windsurf", "Windsurf"},
	{"gemini-cli", "Gemini CLI"},
	{"cline", "Cline"},
	{"warp", "Warp"},
	{"rovo", "Rovo Dev"},
	{"amp", "Amp"},
}

// legacySkillDirs 返回旧版本装到 agent 私有目录、现已被通用目录取代的安装路径。
// 这些 agent 都会读取 ~/.agents/skills，留着旧副本会让同一个 opsctl skill 出现两份，
// 且旧副本不再被更新（Gemini 扩展的 GEMINI.md 还会无条件注入上下文）。
func legacySkillDirs(home string) []string {
	return []string{
		filepath.Join(home, ".codex", "skills", pluginName),
		filepath.Join(home, ".gemini", "extensions", pluginName),
		filepath.Join(home, ".config", "opencode", "skills", pluginName),
	}
}

// universalDef 通用目录安装目标
var universalDef = skillTargetDef{
	Key: universalSkillKey, Name: "Universal", Type: installSkill,
	SkillFn:  func(home string) string { return universalSkillDir(home) },
	DetectFn: skillInstalledAt,
}

// skillInstalledAt 检测目录是否已安装 SKILL.md
func skillInstalledAt(path string) bool {
	_, err := os.Stat(filepath.Join(path, "SKILL.md"))
	return err == nil
}

// standaloneDefs 不读取 ~/.agents/skills、需要单独安装的目标
var standaloneDefs = []skillTargetDef{
	{
		"claude-code", "Claude Code", installClaude,
		func(home string) string { return claudePluginDir(home) },
		func(path string) bool {
			_, err := os.Stat(filepath.Join(path, ".claude-plugin", "plugin.json"))
			return err == nil
		},
	},
}

const pluginRegistryName = "opskat"
const pluginName = "opsctl"
const pluginVersion = "1.1.0"

// claudePluginDir 返回 Claude Code 插件目录（marketplace 内的插件根目录）
func claudePluginDir(home string) string {
	return filepath.Join(home, ".claude", "plugins", "marketplaces", pluginRegistryName, pluginName)
}

// pathTraversesSymlink 检测 path 自身或其任一祖先是否为软链接。
// 用于安装阶段：开发者会把 ~/.claude/plugins/marketplaces/opskat 软链到源码 plugin/，
// 这时不应该把动态注入后的 SKILL.md 写回源码树。
func pathTraversesSymlink(p string) bool {
	p = filepath.Clean(p)
	for {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// claudeMarketplaceDir 返回 Claude Code 市场目录
func claudeMarketplaceDir(home string) string {
	return filepath.Join(home, ".claude", "plugins", "marketplaces", pluginRegistryName)
}

// DetectSkills 检测通用目录与单独安装目标的 Skill 安装状态
func (s *System) DetectSkills() SkillInstallInfo {
	home, err := os.UserHomeDir()
	info := SkillInstallInfo{UniversalAgents: universalAgents}
	if err != nil {
		return info
	}

	upath := universalSkillDir(home)
	info.UniversalPath = upath
	info.UniversalInstalled = universalDef.DetectFn(upath)

	info.Standalone = make([]SkillTarget, 0, len(standaloneDefs))
	for _, def := range standaloneDefs {
		path := def.SkillFn(home)
		info.Standalone = append(info.Standalone, SkillTarget{
			Key:       def.Key,
			Name:      def.Name,
			Installed: def.DetectFn(path),
			Path:      path,
		})
	}
	return info
}

// skillMDWithDataDir 返回注入数据目录后的 SKILL.md 内容
func (s *System) skillMDWithDataDir() string {
	dataDir := bootstrap.AppDataDir()
	insertion := "## Data Directory\n\n" + dataDir + "\n\n"
	return strings.Replace(s.skillContent.SkillMD, "## Global Flags", insertion+"## Global Flags", 1)
}

// installPluginTo 将 Skill 以插件格式安装到 Claude Code
// pluginDir 是 marketplace 内的插件根目录（marketplaces/opskat/opsctl/）
func (s *System) installPluginTo(pluginDir, home string) error {
	if pathTraversesSymlink(pluginDir) {
		logger.Default().Info("skip Claude plugin install: target traverses symlink (dev mode)", zap.String("path", pluginDir))
		return nil
	}
	// 创建插件目录结构（插件和市场 manifest 都在 marketplace 目录树内）
	mktDir := claudeMarketplaceDir(home)
	dirs := []string{
		filepath.Join(pluginDir, ".claude-plugin"),
		filepath.Join(pluginDir, "skills", "opsctl", "references"),
		filepath.Join(pluginDir, "commands"),
		filepath.Join(mktDir, ".claude-plugin"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create directory %s failed: %w", d, err)
		}
	}

	// 写入插件文件
	files := map[string]string{
		filepath.Join(pluginDir, ".claude-plugin", "plugin.json"):                 s.skillContent.PluginJSON,
		filepath.Join(pluginDir, ".claude-plugin", "marketplace.json"):            s.skillContent.PluginMarketplaceJSON,
		filepath.Join(pluginDir, "skills", "opsctl", "SKILL.md"):                  s.skillMDWithDataDir(),
		filepath.Join(pluginDir, "skills", "opsctl", "references", "commands.md"): s.skillContent.CommandsMD,
		filepath.Join(pluginDir, "commands", "init.md"):                           s.skillContent.InitMD,
		// 市场根目录 manifest
		filepath.Join(mktDir, ".claude-plugin", "marketplace.json"): s.skillContent.MarketplaceJSON,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s failed: %w", filepath.Base(path), err)
		}
	}

	// 注册到 installed_plugins.json + known_marketplaces.json + settings.json
	if err := s.registerPlugin(home); err != nil {
		return fmt.Errorf("register plugin failed: %w", err)
	}

	return nil
}

// registerPlugin 注册插件到 installed_plugins.json + known_marketplaces.json + settings.json
func (s *System) registerPlugin(home string) error {
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	key := pluginName + "@" + pluginRegistryName
	pluginPath := claudePluginDir(home) // marketplaces/opskat/opsctl
	mktPath := claudeMarketplaceDir(home)

	// 1. installed_plugins.json
	type pluginEntry struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
		Version     string `json:"version"`
		InstalledAt string `json:"installedAt"`
		LastUpdated string `json:"lastUpdated"`
	}
	type pluginsConfig struct {
		Version int                      `json:"version"`
		Plugins map[string][]pluginEntry `json:"plugins"`
	}

	pluginsFile := filepath.Join(pluginsDir, "installed_plugins.json")
	cfg := pluginsConfig{Version: 2, Plugins: make(map[string][]pluginEntry)}
	if data, err := os.ReadFile(pluginsFile); err == nil { //nolint:gosec // path from app data dir
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Default().Warn("parse installed_plugins.json failed, will overwrite", zap.Error(err))
			cfg = pluginsConfig{Version: 2, Plugins: make(map[string][]pluginEntry)}
		}
	}

	entries := cfg.Plugins[key]
	found := false
	for i, e := range entries {
		if e.Scope == "user" {
			entries[i].InstallPath = pluginPath
			entries[i].Version = pluginVersion
			entries[i].LastUpdated = now
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, pluginEntry{
			Scope:       "user",
			InstallPath: pluginPath,
			Version:     pluginVersion,
			InstalledAt: now,
			LastUpdated: now,
		})
	}
	cfg.Plugins[key] = entries
	if err := writeJSON(pluginsFile, cfg); err != nil {
		return fmt.Errorf("write installed_plugins.json: %w", err)
	}

	// 2. known_marketplaces.json
	kmFile := filepath.Join(pluginsDir, "known_marketplaces.json")
	km := make(map[string]any)
	if data, err := os.ReadFile(kmFile); err == nil { //nolint:gosec // path from app data dir
		if err := json.Unmarshal(data, &km); err != nil {
			logger.Default().Warn("parse known_marketplaces.json failed, will overwrite", zap.Error(err))
			km = make(map[string]any)
		}
	}
	km[pluginRegistryName] = map[string]any{
		"source":          map[string]any{"source": "directory", "path": mktPath},
		"installLocation": mktPath,
		"lastUpdated":     now,
	}
	if err := writeJSON(kmFile, km); err != nil {
		return fmt.Errorf("write known_marketplaces.json: %w", err)
	}

	// 3. settings.json — enabledPlugins + extraKnownMarketplaces
	settingsFile := filepath.Join(home, ".claude", "settings.json")
	sc := make(map[string]any)
	if data, err := os.ReadFile(settingsFile); err == nil { //nolint:gosec // path from app data dir
		if err := json.Unmarshal(data, &sc); err != nil {
			logger.Default().Warn("parse settings.json failed, will overwrite plugin settings", zap.Error(err))
			sc = make(map[string]any)
		}
	}
	ep, _ := sc["enabledPlugins"].(map[string]any)
	if ep == nil {
		ep = make(map[string]any)
	}
	ep[key] = true
	sc["enabledPlugins"] = ep

	ekm, _ := sc["extraKnownMarketplaces"].(map[string]any)
	if ekm == nil {
		ekm = make(map[string]any)
	}
	ekm[pluginRegistryName] = map[string]any{
		"source": map[string]any{"source": "directory", "path": mktPath},
	}
	sc["extraKnownMarketplaces"] = ekm
	if err := writeJSON(settingsFile, sc); err != nil {
		return fmt.Errorf("write settings.json: %w", err)
	}

	return nil
}

// writeJSON 将数据写入 JSON 文件
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// installSkillTo 将 Skill 文件以普通 Agent Skills 格式安装到目标目录
// 这类工具没有 commands/ 机制，init.md 作为 references 供自动加载
func (s *System) installSkillTo(skillDir string) error {
	if pathTraversesSymlink(skillDir) {
		logger.Default().Info("skip skill install: target traverses symlink (dev mode)", zap.String("path", skillDir))
		return nil
	}
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0755); err != nil {
		return fmt.Errorf("create directory failed: %w", err)
	}

	files := map[string]string{
		filepath.Join(skillDir, "SKILL.md"):   s.skillMDWithDataDir(),
		filepath.Join(refsDir, "commands.md"): s.skillContent.CommandsMD,
		filepath.Join(refsDir, "init.md"):     s.skillContent.InitMD,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s failed: %w", filepath.Base(path), err)
		}
	}

	return nil
}

// installTarget 根据安装类型分发到对应安装方法
func (s *System) installTarget(def skillTargetDef, home string) error {
	path := def.SkillFn(home)
	switch def.Type {
	case installClaude:
		return s.installPluginTo(path, home)
	case installSkill:
		return s.installSkillTo(path)
	default:
		return fmt.Errorf("unknown install type: %d", def.Type)
	}
}

func (s *System) skillTargetCurrent(def skillTargetDef, home string) (bool, error) {
	root := def.SkillFn(home)
	expected := map[string]string{}
	switch def.Type {
	case installClaude:
		expected[filepath.Join(root, ".claude-plugin", "plugin.json")] = s.skillContent.PluginJSON
		expected[filepath.Join(root, ".claude-plugin", "marketplace.json")] = s.skillContent.PluginMarketplaceJSON
		expected[filepath.Join(root, "skills", "opsctl", "SKILL.md")] = s.skillMDWithDataDir()
		expected[filepath.Join(root, "skills", "opsctl", "references", "commands.md")] = s.skillContent.CommandsMD
		expected[filepath.Join(root, "commands", "init.md")] = s.skillContent.InitMD
	case installSkill:
		expected[filepath.Join(root, "SKILL.md")] = s.skillMDWithDataDir()
		expected[filepath.Join(root, "references", "commands.md")] = s.skillContent.CommandsMD
		expected[filepath.Join(root, "references", "init.md")] = s.skillContent.InitMD
	default:
		return false, fmt.Errorf("unknown install type: %d", def.Type)
	}
	for path, want := range expected {
		got, err := os.ReadFile(path) //nolint:gosec // path is a known skill target under the user home
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read %s: %w", path, err)
		}
		if string(got) != want {
			return false, nil
		}
	}
	return true, nil
}

// allSkillDefs 返回全部安装目标（通用目录 + 单独安装）
func allSkillDefs() []skillTargetDef {
	return append([]skillTargetDef{universalDef}, standaloneDefs...)
}

// migrateLegacySkills 把旧版本装在 agent 私有目录的 Skill 收敛到通用目录：
// 先确保通用目录已安装，再删除旧副本。通用目录没装成功（开发模式软链接）时保留旧副本，
// 否则会把用户当下唯一可用的安装删掉。
func (s *System) migrateLegacySkills(home string) error {
	legacy := legacySkillDirs(home)
	stale := make([]string, 0, len(legacy))
	for _, dir := range legacy {
		if _, err := os.Stat(dir); err == nil {
			stale = append(stale, dir)
		}
	}
	if len(stale) == 0 {
		return nil
	}

	universalPath := universalSkillDir(home)
	if !universalDef.DetectFn(universalPath) {
		if err := s.installTarget(universalDef, home); err != nil {
			return fmt.Errorf("install universal skill for migration: %w", err)
		}
		if !universalDef.DetectFn(universalPath) {
			logger.Default().Info("skip legacy skill migration: universal skill install was skipped",
				zap.String("path", universalPath))
			return nil
		}
	}

	for _, dir := range stale {
		if err := removeOwnedDir(dir, home, pluginName); err != nil {
			return fmt.Errorf("remove legacy skill dir %s: %w", dir, err)
		}
		logger.Default().Info("migrated legacy skill install to universal dir",
			zap.String("removed", dir), zap.String("universal", universalPath))
	}
	return nil
}

func (s *System) updateInstalledSkills(home string) error {
	if err := s.migrateLegacySkills(home); err != nil {
		return err
	}
	for _, def := range allSkillDefs() {
		if !def.DetectFn(def.SkillFn(home)) {
			continue
		}
		current, err := s.skillTargetCurrent(def, home)
		if err != nil {
			return fmt.Errorf("check %s update: %w", def.Name, err)
		}
		if current {
			continue
		}
		logger.Default().Info("updating installed skill", zap.String("target", def.Key))
		if err := s.installTarget(def, home); err != nil {
			return fmt.Errorf("update %s: %w", def.Name, err)
		}
		logger.Default().Info("updated installed skill", zap.String("target", def.Key))
	}
	return nil
}

func (s *System) updateInstalledTools() {
	opsctlInfo := s.DetectOpsctl()
	if opsctlInfo.Installed && opsctlInfo.Embedded {
		needsUpdate, err := embedded.OpsctlNeedsUpdate(opsctlInfo.Path)
		if err != nil {
			logger.Default().Warn("check installed opsctl update failed", zap.Error(err))
		} else if needsUpdate {
			logger.Default().Info("updating installed opsctl", zap.String("path", opsctlInfo.Path))
			if _, err := embedded.InstallOpsctl(filepath.Dir(opsctlInfo.Path)); err != nil {
				// 重装失败不阻塞启动，但要通知用户——事件由 App.tsx 全局监听（重装在启动时
				// 触发，设置页未必挂载，不能只靠 UpdateSection 的监听器），否则失败完全无感知。
				logger.Default().Warn("update installed opsctl failed", zap.Error(err))
				wailsRuntime.EventsEmit(s.ctx, "update:opsctl-error", err.Error())
			} else {
				logger.Default().Info("updated installed opsctl", zap.String("path", opsctlInfo.Path))
			}
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		logger.Default().Warn("get home directory for skill update failed", zap.Error(err))
		return
	}
	if err := s.updateInstalledSkills(home); err != nil {
		logger.Default().Warn("update installed skill failed", zap.Error(err))
		wailsRuntime.EventsEmit(s.ctx, "update:skill-error", err.Error())
	}
}

// InstallSkills 安装 Skill 到通用目录（~/.agents/skills）及所有单独安装目标
func (s *System) InstallSkills() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory failed: %w", err)
	}

	for _, def := range allSkillDefs() {
		if err := s.installTarget(def, home); err != nil {
			return fmt.Errorf("install %s failed: %w", def.Name, err)
		}
	}

	if err := s.migrateLegacySkills(home); err != nil {
		return err
	}

	// 在应用数据目录写一份各工具的插件结构，方便用户手动拷贝
	if err := s.writePluginReference(); err != nil {
		logger.Default().Warn("write plugin reference failed", zap.Error(err))
	}

	return nil
}

// InstallSkillTarget 单独安装指定目标（universal / claude-code / opencode）
func (s *System) InstallSkillTarget(key string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory failed: %w", err)
	}

	for _, def := range allSkillDefs() {
		if def.Key == key {
			return s.installTarget(def, home)
		}
	}
	return fmt.Errorf("unknown skill target: %s", key)
}

// UninstallSkill 卸载指定 AI 工具中的 opsctl Skill/插件。
func (s *System) UninstallSkill(key string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory failed: %w", err)
	}

	for _, def := range allSkillDefs() {
		if def.Key != key {
			continue
		}
		if err := s.uninstallTarget(def, home); err != nil {
			return fmt.Errorf("uninstall %s failed: %w", def.Name, err)
		}
		return nil
	}
	return fmt.Errorf("unknown skill target: %s", key)
}

func (s *System) uninstallTarget(def skillTargetDef, home string) error {
	path := def.SkillFn(home)
	if def.Type == installClaude {
		if err := removeOwnedDir(claudeMarketplaceDir(home), home, pluginRegistryName); err != nil {
			return err
		}
		return s.unregisterClaudePlugin(home)
	}
	if err := removeOwnedDir(path, home, pluginName); err != nil {
		return err
	}
	return nil
}

func removeOwnedDir(path, home, expectedBase string) error {
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)
	if filepath.Base(cleanPath) != expectedBase {
		return fmt.Errorf("refuse to remove non-%s path: %s", expectedBase, cleanPath)
	}
	rel, err := filepath.Rel(cleanHome, cleanPath)
	if err != nil {
		return fmt.Errorf("check target path: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("refuse to remove path outside home: %s", cleanPath)
	}
	if pathTraversesSymlink(cleanPath) {
		// 开发模式下目录可能被软链到源码树（与安装侧一致），跳过删除而非删穿软链接。
		logger.Default().Info("skip skill uninstall: target traverses symlink (dev mode)", zap.String("path", cleanPath))
		return nil
	}
	if err := os.RemoveAll(cleanPath); err != nil {
		return fmt.Errorf("remove %s failed: %w", cleanPath, err)
	}
	return nil
}

func (s *System) unregisterClaudePlugin(home string) error {
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	key := pluginName + "@" + pluginRegistryName

	if err := removeClaudeInstalledPlugin(filepath.Join(pluginsDir, "installed_plugins.json"), key); err != nil {
		return err
	}
	if err := removeJSONTopLevelKey(filepath.Join(pluginsDir, "known_marketplaces.json"), pluginRegistryName); err != nil {
		return err
	}
	if err := removeClaudeSettingsPlugin(filepath.Join(home, ".claude", "settings.json"), key); err != nil {
		return err
	}
	return nil
}

func removeClaudeInstalledPlugin(path, key string) error {
	type pluginEntry struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
		Version     string `json:"version"`
		InstalledAt string `json:"installedAt"`
		LastUpdated string `json:"lastUpdated"`
	}
	type pluginsConfig struct {
		Version int                      `json:"version"`
		Plugins map[string][]pluginEntry `json:"plugins"`
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is under user home config
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read installed_plugins.json: %w", err)
	}
	cfg := pluginsConfig{Version: 2, Plugins: make(map[string][]pluginEntry)}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse installed_plugins.json: %w", err)
	}
	entries := cfg.Plugins[key]
	kept := entries[:0]
	for _, entry := range entries {
		if entry.Scope != "user" {
			kept = append(kept, entry)
		}
	}
	if len(kept) == 0 {
		delete(cfg.Plugins, key)
	} else {
		cfg.Plugins[key] = kept
	}
	if err := writeJSON(path, cfg); err != nil {
		return fmt.Errorf("write installed_plugins.json: %w", err)
	}
	return nil
}

func removeJSONTopLevelKey(path, key string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is under user home config
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	value := make(map[string]any)
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	delete(value, key)
	if err := writeJSON(path, value); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func removeClaudeSettingsPlugin(path, key string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is under user home config
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read settings.json: %w", err)
	}
	settings := make(map[string]any)
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse settings.json: %w", err)
	}
	if enabled, ok := settings["enabledPlugins"].(map[string]any); ok {
		delete(enabled, key)
	}
	if marketplaces, ok := settings["extraKnownMarketplaces"].(map[string]any); ok {
		delete(marketplaces, pluginRegistryName)
	}
	if err := writeJSON(path, settings); err != nil {
		return fmt.Errorf("write settings.json: %w", err)
	}
	return nil
}

// GetPluginReferenceDir 返回应用数据目录下的插件参考目录
func (s *System) GetPluginReferenceDir() string {
	return filepath.Join(bootstrap.AppDataDir(), "plugins")
}

// writePluginReference 在数据目录写各工具的插件目录结构
func (s *System) writePluginReference() error {
	base := s.GetPluginReferenceDir()
	skillMD := s.skillMDWithDataDir()

	structures := []struct {
		files map[string]string
	}{
		// Universal（~/.agents/skills 布局，一次安装多 agent 生效）
		{files: map[string]string{
			filepath.Join(base, "agents", "opsctl", "SKILL.md"):                  skillMD,
			filepath.Join(base, "agents", "opsctl", "references", "commands.md"): s.skillContent.CommandsMD,
			filepath.Join(base, "agents", "opsctl", "references", "init.md"):     s.skillContent.InitMD,
		}},
		// Claude Code
		{files: map[string]string{
			filepath.Join(base, "claude-code", ".claude-plugin", "marketplace.json"):                      s.skillContent.MarketplaceJSON,
			filepath.Join(base, "claude-code", "opsctl", ".claude-plugin", "plugin.json"):                 s.skillContent.PluginJSON,
			filepath.Join(base, "claude-code", "opsctl", ".claude-plugin", "marketplace.json"):            s.skillContent.PluginMarketplaceJSON,
			filepath.Join(base, "claude-code", "opsctl", "skills", "opsctl", "SKILL.md"):                  skillMD,
			filepath.Join(base, "claude-code", "opsctl", "skills", "opsctl", "references", "commands.md"): s.skillContent.CommandsMD,
			filepath.Join(base, "claude-code", "opsctl", "commands", "init.md"):                           s.skillContent.InitMD,
		}},
	}

	for _, struc := range structures {
		for p, content := range struc.files {
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(content), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetSkillPreview 获取 Skill 文件内容预览
func (s *System) GetSkillPreview() string {
	return "--- skills/opsctl/SKILL.md ---\n\n" + s.skillMDWithDataDir() +
		"\n\n--- commands/init.md ---\n\n" + s.skillContent.InitMD +
		"\n\n--- skills/opsctl/references/commands.md ---\n\n" + s.skillContent.CommandsMD
}

// --- 审计日志 ---

// AuditLogListResult 审计日志列表结果
type AuditLogListResult struct {
	Items []*audit_entity.AuditLog `json:"items"`
	Total int64                    `json:"total"`
}

// ListAuditLogs 查询审计日志
func (s *System) ListAuditLogs(source string, assetID int64, startTime, endTime int64, offset, limit int, sessionID string) (*AuditLogListResult, error) {
	if limit <= 0 {
		limit = 20
	}
	items, total, err := audit_repo.Audit().List(i18n.Ctx(s.ctx, s.Lang()), audit_repo.ListOptions{
		Source:    source,
		AssetID:   assetID,
		SessionID: sessionID,
		StartTime: startTime,
		EndTime:   endTime,
		Offset:    offset,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	return &AuditLogListResult{Items: items, Total: total}, nil
}

// ListAuditSessions 查询审计日志中的会话列表
func (s *System) ListAuditSessions(startTime int64) ([]audit_repo.SessionInfo, error) {
	return audit_repo.Audit().ListSessions(i18n.Ctx(s.ctx, s.Lang()), startTime)
}

// --- 更新 ---

// startAutoUpdateCheck 启动时自动检查更新（每天一次）
func (s *System) startAutoUpdateCheck() {
	go func() {
		// 延迟 5 秒，等前端就绪
		time.Sleep(5 * time.Second)
		s.updateInstalledTools()

		// OPSKAT_E2E=1 的验证运行（e2e / 交互沙盒）不做线上更新检查：结果取决于外网
		// 和当时的线上最新版本，上游一发版 update:available 的右下角 toast 就会盖住
		// composer，让 e2e 随发布日历随机失败（真实案例：v1.12.1 发布当天 ai-exec-crud
		// 被 "New version" toast 挡住输入框超时）。
		if os.Getenv("OPSKAT_E2E") == "1" {
			return
		}

		cfg := bootstrap.GetConfig()
		if cfg == nil {
			return
		}
		now := time.Now().Unix()
		if now-cfg.LastUpdateCheck < 86400 {
			return
		}

		info, err := update_svc.CheckForUpdate(s.GetUpdateChannel(), s.GetDownloadMirror())
		if err != nil {
			logger.Default().Warn("auto check update failed", zap.Error(err))
			return
		}

		cfg.LastUpdateCheck = now
		if err := bootstrap.SaveConfig(cfg); err != nil {
			logger.Default().Warn("save last update check time", zap.Error(err))
		}

		if info.HasUpdate {
			wailsRuntime.EventsEmit(s.ctx, "update:available", info)
		}
	}()
}

// GetAppVersion 返回当前应用版本
func (s *System) GetAppVersion() string {
	v := configs.Version
	if c := buildinfo.ShortCommitID(); c != "" {
		v += " (" + c + ")"
	}
	return v
}

// BugReportInfo 用于前端拼接 GitHub Issue 预填 URL 的诊断信息。
type BugReportInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	OSLabel string `json:"osLabel"`
}

// GetDebugMode 返回当前是否开启 debug 日志
func (s *System) GetDebugMode() bool {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return false
	}
	return cfg.DebugMode
}

// SetDebugMode 开启/关闭 debug 日志，写入配置并重建全局 logger
func (s *System) SetDebugMode(enabled bool) error {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	cfg.DebugMode = enabled
	if err := bootstrap.SaveConfig(cfg); err != nil {
		return err
	}
	return bootstrap.InitLogger()
}

// OpenLogsDir 在系统文件管理器中打开日志目录
func (s *System) OpenLogsDir() error {
	dir := bootstrap.GetLogsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return s.OpenDirectory(dir)
}

// GetBugReportInfo 返回用于 Bug 反馈模板预填的系统信息。
func (s *System) GetBugReportInfo() BugReportInfo {
	osVer := detectOSVersion()
	archSuffix := runtime.GOARCH
	osLabel := ""
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			archSuffix = "Apple Silicon"
		} else {
			archSuffix = "Intel"
		}
		if osVer != "" {
			osLabel = fmt.Sprintf("macOS %s (%s)", osVer, archSuffix)
		} else {
			osLabel = fmt.Sprintf("macOS (%s)", archSuffix)
		}
	case "windows":
		if osVer != "" {
			osLabel = fmt.Sprintf("Windows %s (%s)", osVer, archSuffix)
		} else {
			osLabel = fmt.Sprintf("Windows (%s)", archSuffix)
		}
	case "linux":
		if osVer != "" {
			osLabel = fmt.Sprintf("%s (%s)", osVer, archSuffix)
		} else {
			osLabel = fmt.Sprintf("Linux (%s)", archSuffix)
		}
	default:
		osLabel = fmt.Sprintf("%s (%s)", runtime.GOOS, archSuffix)
	}
	return BugReportInfo{
		Version: configs.Version,
		Commit:  buildinfo.ShortCommitID(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		OSLabel: osLabel,
	}
}

// detectOSVersion 尝试获取当前操作系统版本号/发行版名称，失败返回空串。
func detectOSVersion() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	case "linux":
		data, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return ""
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if name, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				return strings.Trim(strings.TrimSpace(name), `"`)
			}
		}
		return ""
	case "windows":
		cmd := exec.Command("cmd", "/c", "ver")
		executil.HideConsoleWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		out2 := strings.TrimSpace(string(out))
		if i := strings.Index(out2, "[Version "); i >= 0 {
			if j := strings.Index(out2[i:], "]"); j > 0 {
				return strings.TrimPrefix(out2[i:i+j], "[Version ")
			}
		}
		return ""
	}
	return ""
}

// GetUpdateChannel 获取当前更新通道
func (s *System) GetUpdateChannel() string {
	cfg := bootstrap.GetConfig()
	if cfg == nil || cfg.UpdateChannel == "" {
		return update_svc.ChannelStable
	}
	return cfg.UpdateChannel
}

// SetUpdateChannel 设置更新通道
func (s *System) SetUpdateChannel(channel string) error {
	cfg := bootstrap.GetConfig()
	cfg.UpdateChannel = channel
	return bootstrap.SaveConfig(cfg)
}

// GetDownloadMirror 获取当前下载镜像 URL 前缀
func (s *System) GetDownloadMirror() string {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return ""
	}
	return cfg.DownloadMirror
}

// SetDownloadMirror 设置下载镜像
// mirror 为镜像 URL 前缀（如 "https://ghfast.top/"），空字符串表示直连 GitHub
func (s *System) SetDownloadMirror(mirror string) error {
	cfg := bootstrap.GetConfig()
	cfg.DownloadMirror = mirror
	return bootstrap.SaveConfig(cfg)
}

// GetAvailableMirrors 返回可用的下载镜像列表
func (s *System) GetAvailableMirrors() []update_svc.MirrorInfo {
	return update_svc.GetAvailableMirrors()
}

// CheckForUpdate 检查是否有新版本
func (s *System) CheckForUpdate() (*update_svc.UpdateInfo, error) {
	return update_svc.CheckForUpdate(s.GetUpdateChannel(), s.GetDownloadMirror())
}

// DownloadAndInstallUpdate 下载并安装更新
// 更新完成后需要用户重启应用
func (s *System) DownloadAndInstallUpdate(skipChecksum bool) error {
	err := update_svc.DownloadAndUpdate(s.GetUpdateChannel(), s.GetDownloadMirror(), skipChecksum, func(downloaded, total int64) {
		wailsRuntime.EventsEmit(s.ctx, "update:progress", map[string]int64{
			"downloaded": downloaded,
			"total":      total,
		})
	})
	if err != nil {
		return err
	}

	return nil
}
