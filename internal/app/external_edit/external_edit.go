// Package external_edit 实现远程文件外部编辑 binder：
// 仅做依赖装配和 IPC 转发，状态机/编码/审计等业务逻辑都下沉到 external_edit_svc。
package external_edit

import (
	"context"
	"os/exec"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/executil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/service/external_edit_svc"
	"github.com/opskat/opskat/internal/service/sftp_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// LangProvider 由 system binder 实现，提供当前 UI 语言。
type LangProvider interface {
	Lang() string
}

// Type aliases 让 Wails 在生成 TS binding 时把 service 内部类型挂在本包下，
// 前端只需要 import 一个稳定的 external_edit 命名空间。
type (
	Settings           = external_edit_svc.Settings
	SettingsInput      = external_edit_svc.SettingsInput
	OpenRequest        = external_edit_svc.OpenRequest
	Session            = external_edit_svc.Session
	SaveResult         = external_edit_svc.SaveResult
	CompareResult      = external_edit_svc.CompareResult
	MergePrepareResult = external_edit_svc.MergePrepareResult
	MergeApplyRequest  = external_edit_svc.MergeApplyRequest
)

// ExternalEdit 外部编辑 binder：仅依赖 sftpSvc + sshMgr，不直接持有数据库。
type ExternalEdit struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	sftpSvc *sftp_svc.Service
	sshMgr  *ssh_svc.Manager

	svc *external_edit_svc.Service
}

// New 构造 external edit binder。sftpSvc/sshMgr 由 main.go 创建后注入。
func New(appCtx context.Context, lang LangProvider, sftpSvc *sftp_svc.Service, sshMgr *ssh_svc.Manager) *ExternalEdit {
	return &ExternalEdit{
		appCtx:  appCtx,
		lang:    lang,
		sftpSvc: sftpSvc,
		sshMgr:  sshMgr,
	}
}

// Startup 保存 Wails ctx 后再启动 service：Emit 回调依赖 ctx 才能 EventsEmit。
func (e *ExternalEdit) Startup(ctx context.Context) {
	e.ctx = ctx
	svc, err := external_edit_svc.NewService(external_edit_svc.Options{
		DataDir:        bootstrap.AppDataDir(),
		ConfigProvider: bootstrap.GetConfig,
		ConfigSaver:    bootstrap.SaveConfig,
		Remote:         e.sftpSvc,
		FindSessions:   e.sshMgr.ListActiveSessionIDsByAsset,
		Assets:         asset_repo.Asset(),
		Audit:          audit_repo.Audit(),
		Emit: func(event external_edit_svc.Event) {
			if e.ctx == nil {
				return
			}
			wailsRuntime.EventsEmit(e.ctx, "external-edit:event", event)
		},
		Launch: launcher{},
	})
	if err != nil {
		logger.Default().Warn("init external edit service", zap.Error(err))
		return
	}
	if err := svc.Start(context.Background()); err != nil {
		logger.Default().Warn("start external edit service", zap.Error(err))
	}
	e.svc = svc
}

// Cleanup 关闭 service：watcher / 后台 goroutine / 文件句柄都在 service.Close 里收口。
func (e *ExternalEdit) Cleanup() {
	if e.svc == nil {
		return
	}
	if err := e.svc.Close(); err != nil {
		logger.Default().Warn("close external edit service", zap.Error(err))
	}
}

// launcher 在桌面端启动外部编辑器进程，Windows 下隐藏控制台窗口。
type launcher struct{}

func (launcher) Launch(execPath string, args []string) error {
	cmd := exec.Command(execPath, args...) //nolint:gosec // path 与 args 已在 external_edit_svc 内校验
	executil.HideConsoleWindow(cmd)
	return cmd.Start()
}
