package auto_backup_svc

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

var (
	defaultOnce    sync.Once
	defaultService *Service
)

// Default 返回进程内唯一的自动备份服务（首次调用时读取数据目录下的运行状态）。
func Default() *Service {
	defaultOnce.Do(func() {
		defaultService = New(Options{
			Now:          time.Now,
			LoadSettings: func() (Settings, error) { return LoadSettings(credential_svc.Default()) },
			Export:       exportAll,
			Targets:      Targets(),
			StatePath:    filepath.Join(bootstrap.ResolvedDataDir(), "auto_backup_state.json"),
		})
	})
	return defaultService
}

// exportAll 生成完整备份：资产、分组、凭据、策略组、端口转发、快捷键、终端主题。
func exportAll(ctx context.Context, snap ClientSnapshot) (*backup_svc.BackupData, error) {
	return backup_svc.Export(ctx, &backup_svc.ExportOptions{
		IncludeCredentials:  true,
		IncludeForwards:     true,
		IncludePolicyGroups: true,
		IncludeShortcuts:    true,
		IncludeThemes:       true,
		Shortcuts:           snap.Shortcuts,
		CustomThemes:        snap.CustomThemes,
	}, credential_svc.Default())
}
