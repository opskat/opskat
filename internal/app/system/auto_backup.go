package system

import (
	"fmt"
	"slices"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/auto_backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// autoBackupStatusEvent 通知前端自动备份状态变了（无负载，前端重新读取状态）。
const autoBackupStatusEvent = "autobackup:status"

// AutoBackupSettings 是设置页读取的自动备份设置；不返回密码，只返回是否已设置。
type AutoBackupSettings struct {
	Enabled         bool     `json:"enabled"`
	PasswordSet     bool     `json:"passwordSet"`
	IntervalMinutes int      `json:"intervalMinutes"`
	Keep            int      `json:"keep"`
	Targets         []string `json:"targets"`
	LocalDir        string   `json:"localDir"`
}

// AutoBackupSaveInput 是保存自动备份设置的入参；Password 为空表示保留已保存的密码。
type AutoBackupSaveInput struct {
	Enabled         bool     `json:"enabled"`
	Password        string   `json:"password"`
	IntervalMinutes int      `json:"intervalMinutes"`
	Keep            int      `json:"keep"`
	Targets         []string `json:"targets"`
	LocalDir        string   `json:"localDir"`
}

// AutoBackupTargetStatus 是一个备份位置在设置页上的状态。
type AutoBackupTargetStatus struct {
	ID            string `json:"id"`
	Available     bool   `json:"available"` // 当前已配置好（WebDAV 已配置、GitHub 已登录、已选文件夹）
	Phase         string `json:"phase"`     // "" | queued | running
	LastSuccessAt int64  `json:"lastSuccessAt"`
	LastError     string `json:"lastError"`
	LastErrorAt   int64  `json:"lastErrorAt"`
	RetryAt       int64  `json:"retryAt"`
}

// AutoBackupStatus 是设置页显示的运行状态。
type AutoBackupStatus struct {
	Running         bool                     `json:"running"`
	LastCheckAt     int64                    `json:"lastCheckAt"`
	LastCheckResult string                   `json:"lastCheckResult"`
	LastCheckError  string                   `json:"lastCheckError"`
	NextCheckAt     int64                    `json:"nextCheckAt"`
	Targets         []AutoBackupTargetStatus `json:"targets"`
	WebDAVURL       string                   `json:"webdavUrl"`
	GitHubUser      string                   `json:"githubUser"`
}

func (s *System) autoBackupService() *auto_backup_svc.Service {
	if s.autoBackup == nil {
		s.autoBackup = auto_backup_svc.Default()
	}
	return s.autoBackup
}

// startAutoBackup 在 Startup 时启动后台调度，状态变化推送给前端。
func (s *System) startAutoBackup() {
	svc := s.autoBackupService()
	svc.SetNotifier(func() { s.emitEvent(autoBackupStatusEvent) })
	svc.Start(s.appCtx)
}

// notifyAutoBackupAvailability 在 WebDAV / GitHub 配置变化后通知前端刷新位置的可用性。
// Startup 之前没有前端在听，不发事件。
func (s *System) notifyAutoBackupAvailability() {
	if s.ctx == nil && s.emit == nil {
		return
	}
	s.emitEvent(autoBackupStatusEvent)
}

// GetAutoBackupSettings 读取自动备份设置。
func (s *System) GetAutoBackupSettings() (*AutoBackupSettings, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}
	targets := cfg.AutoBackupTargets
	if targets == nil {
		targets = []string{}
	}
	return &AutoBackupSettings{
		Enabled:         cfg.AutoBackupEnabled,
		PasswordSet:     cfg.AutoBackupPassword != "",
		IntervalMinutes: auto_backup_svc.IntervalMinutesOrDefault(cfg.AutoBackupIntervalMinutes),
		Keep:            auto_backup_svc.KeepOrDefault(cfg.AutoBackupKeep),
		Targets:         targets,
		LocalDir:        cfg.AutoBackupLocalDir,
	}, nil
}

// SaveAutoBackupSettings 校验并保存自动备份设置，保存后立即检查一次并从此刻起按间隔计时。
// 已勾选但当前不可用的位置（WebDAV 未配置 / GitHub 未登录）保留勾选，检查时跳过。
func (s *System) SaveAutoBackupSettings(in AutoBackupSaveInput) error {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	if !slices.Contains(auto_backup_svc.IntervalChoices, in.IntervalMinutes) {
		return fmt.Errorf("不支持的检查间隔: %d 分钟", in.IntervalMinutes)
	}
	if !slices.Contains(auto_backup_svc.KeepChoices, in.Keep) {
		return fmt.Errorf("不支持的保留份数: %d", in.Keep)
	}
	byID := map[string]auto_backup_svc.Target{}
	for _, t := range auto_backup_svc.Targets() {
		byID[t.ID()] = t
	}
	settings := auto_backup_svc.Settings{LocalDir: in.LocalDir}
	for _, id := range in.Targets {
		t, ok := byID[id]
		if !ok {
			return fmt.Errorf("未知的备份位置: %s", id)
		}
		if err := t.Validate(settings); err != nil {
			return err
		}
	}
	if in.Enabled {
		if in.Password == "" && cfg.AutoBackupPassword == "" {
			return fmt.Errorf("请设置备份密码")
		}
		usable := false
		for _, id := range in.Targets {
			if byID[id].Available(settings) {
				usable = true
				break
			}
		}
		if !usable {
			return fmt.Errorf("至少选择一个备份位置")
		}
	}

	var encrypted string
	if in.Password != "" {
		var err error
		encrypted, err = credential_svc.Default().Encrypt(in.Password)
		if err != nil {
			return fmt.Errorf("加密自动备份密码失败: %w", err)
		}
	}
	targets := slices.Clone(in.Targets)
	if err := bootstrap.UpdateConfig(func(c *bootstrap.AppConfig) {
		c.AutoBackupEnabled = in.Enabled
		if encrypted != "" {
			c.AutoBackupPassword = encrypted
		}
		c.AutoBackupIntervalMinutes = in.IntervalMinutes
		c.AutoBackupKeep = in.Keep
		c.AutoBackupTargets = targets
		c.AutoBackupLocalDir = in.LocalDir
	}); err != nil {
		return err
	}

	svc := s.autoBackupService()
	if encrypted != "" {
		svc.ResetFingerprints()
	}
	svc.Reschedule()
	return nil
}

// GetAutoBackupStatus 返回各位置的状态与可用性。
func (s *System) GetAutoBackupStatus() (*AutoBackupStatus, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}
	st := s.autoBackupService().Status()
	settings := auto_backup_svc.Settings{LocalDir: cfg.AutoBackupLocalDir}
	out := &AutoBackupStatus{
		Running:         st.Running,
		LastCheckAt:     st.LastCheckAt,
		LastCheckResult: st.LastCheckResult,
		LastCheckError:  st.LastCheckError,
		NextCheckAt:     st.NextCheckAt,
		Targets:         []AutoBackupTargetStatus{},
		WebDAVURL:       cfg.WebDAVURL,
		GitHubUser:      cfg.GitHubUser,
	}
	for _, t := range auto_backup_svc.Targets() {
		ts := AutoBackupTargetStatus{ID: t.ID(), Available: t.Available(settings)}
		if cur, ok := st.Targets[t.ID()]; ok {
			ts.Phase = cur.Phase
			ts.LastSuccessAt = cur.LastSuccessAt
			ts.LastError = cur.LastError
			ts.LastErrorAt = cur.LastErrorAt
			ts.RetryAt = cur.RetryAt
		}
		out.Targets = append(out.Targets, ts)
	}
	return out, nil
}

// RunAutoBackupNow 立即对每个勾选且可用的位置写一个新版本，这一轮结束后返回。
func (s *System) RunAutoBackupNow() error {
	return s.autoBackupService().RunNow(s.appCtx)
}

// SetAutoBackupClientSnapshot 接收前端本地存储里的快捷键自定义项与自定义终端主题（JSON 字符串）。
func (s *System) SetAutoBackupClientSnapshot(shortcuts, customThemes string) {
	s.autoBackupService().SetClientSnapshot(auto_backup_svc.ClientSnapshot{Shortcuts: shortcuts, CustomThemes: customThemes})
}

// RequestAutoBackupCheck 前端在界面改了数据后调用：2 秒防抖后检查一次。
func (s *System) RequestAutoBackupCheck() {
	s.autoBackupService().Nudge()
}

// SelectAutoBackupDirectory 打开系统目录选择对话框；取消时返回空字符串。
func (s *System) SelectAutoBackupDirectory() (string, error) {
	return wailsRuntime.OpenDirectoryDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title:                "选择自动备份文件夹",
		CanCreateDirectories: true,
	})
}
