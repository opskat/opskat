package external_edit

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/external_edit_svc"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (e *ExternalEdit) service() (*external_edit_svc.Service, error) {
	if e.svc == nil {
		return nil, fmt.Errorf("external edit service unavailable")
	}
	return e.svc, nil
}

func (e *ExternalEdit) langCtx() context.Context { return i18n.Ctx(e.ctx, e.lang.Lang()) }

func (e *ExternalEdit) GetExternalEditSettings() (*Settings, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.GetSettings()
}

func (e *ExternalEdit) SaveExternalEditSettings(input SettingsInput) (*Settings, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.SaveSettings(input)
}

// SelectExternalEditorExecutable 弹出文件选择器返回用户挑的绝对路径；
// 真正的可执行性校验留给 service 统一处理，避免桌面端和测试端出现双重规则。
func (e *ExternalEdit) SelectExternalEditorExecutable() (string, error) {
	filePath, err := wailsRuntime.OpenFileDialog(e.ctx, wailsRuntime.OpenDialogOptions{
		Title: e.dialogTitle("editor"),
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Executable", Pattern: executablePattern()},
		},
	})
	if err != nil {
		return "", fmt.Errorf("打开文件对话框失败: %w", err)
	}
	return filePath, nil
}

// SelectExternalEditWorkspaceRoot 工作区目录由前端选择，但最终是否落盘以后端配置逻辑为准。
func (e *ExternalEdit) SelectExternalEditWorkspaceRoot() (string, error) {
	dirPath, err := wailsRuntime.OpenDirectoryDialog(e.ctx, wailsRuntime.OpenDialogOptions{
		Title: e.dialogTitle("workspace"),
	})
	if err != nil {
		return "", fmt.Errorf("打开目录对话框失败: %w", err)
	}
	return dirPath, nil
}

// OpenExternalEdit IPC 边界只转发"打开哪个远程文件"的意图；
// 文本判定、会话复用、编码快照、审计和事件广播全部交由 service 串行裁决。
func (e *ExternalEdit) OpenExternalEdit(req OpenRequest) (*Session, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Open(e.langCtx(), req)
}

func (e *ExternalEdit) ListExternalEditSessions() ([]*Session, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.ListSessions(), nil
}

// SaveExternalEditSession 保存和冲突处理都以 sessionID 作为唯一入口，
// 桌面事件、审计和状态恢复围绕同一份会话记录运转。
func (e *ExternalEdit) SaveExternalEditSession(sessionID string) (*SaveResult, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Save(e.langCtx(), sessionID)
}

func (e *ExternalEdit) RefreshExternalEditSession(sessionID string) (*Session, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Refresh(sessionID)
}

// ResolveExternalEditConflict resolution 只是用户决策信号：overwrite / recreate / reread
// 的副作用和状态迁移全部封装在 service 内，IPC 层不额外拼分支。
func (e *ExternalEdit) ResolveExternalEditConflict(sessionID, resolution string) (*SaveResult, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Resolve(e.langCtx(), sessionID, resolution)
}

// CompareExternalEditSession 只暴露"生成只读差异快照"的能力；
// 编码 / BOM / round-trip 校验与远端身份确认仍由 service 串行裁决。
func (e *ExternalEdit) CompareExternalEditSession(sessionID string) (*CompareResult, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Compare(sessionID)
}

func (e *ExternalEdit) PrepareExternalEditMerge(sessionID string) (*MergePrepareResult, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.PrepareMerge(sessionID)
}

func (e *ExternalEdit) ApplyExternalEditMerge(req MergeApplyRequest) (*SaveResult, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.ApplyMerge(e.langCtx(), req)
}

func (e *ExternalEdit) RecoverExternalEditSession(sessionID string) (*Session, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Recover(sessionID)
}

func (e *ExternalEdit) ContinueExternalEditSession(sessionID string) (*Session, error) {
	svc, err := e.service()
	if err != nil {
		return nil, err
	}
	return svc.Continue(sessionID)
}

func (e *ExternalEdit) dialogTitle(kind string) string {
	isEnglish := strings.EqualFold(e.lang.Lang(), "en")
	switch kind {
	case "workspace":
		if isEnglish {
			return "Choose External Edit Workspace"
		}
		return "选择外部编辑工作区"
	default:
		if isEnglish {
			return "Choose External Editor"
		}
		return "选择外部编辑器"
	}
}

func executablePattern() string {
	if runtime.GOOS == "windows" {
		return "*.exe"
	}
	return "*"
}
