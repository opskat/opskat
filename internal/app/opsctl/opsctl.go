// Package opsctl 实现 opsctl binder：对 opsctl CLI 暴露的 本地 IPC 桥（审批 + 资产）。
//
// Wails 绑定方法：RespondOpsctlApproval（审批）、RespondOpsctlMFA / CancelOpsctlMFA
// （opsctl 转来的 SSH MFA 挑战）；其它都是底层服务。
package opsctl

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// WindowActivator 由 system binder 实现，审批弹窗时把窗口拉到前台。
type WindowActivator interface {
	ActivateWindow()
}

// ExtToolExecutor 在 opsctl Unix socket 收到扩展资产的执行请求时把执行交回桌面进程。
//
// 只有扩展资产走这条路，理由是执行位置而不是语义：WASM 运行时只存在于桌面进程。
// 命令串与 `opsctl exec <asset> -- <command>` 里的完全一样，桌面端跑的也是同一个统一
// exec handler，因此策略、审批、grant、审计与内置类型逐字一致。
type ExtToolExecutor interface {
	ExecuteExtTool(ctx context.Context, assetID int64, command string) (string, error)
}

// ExtDevInstaller 把 `opsctl ext dev` 送来的扩展目录装进运行中的桌面进程，
// 返回装上的扩展名与版本。
//
// 与 ExtToolExecutor 同一条理由——位置而非语义：安装要落 enabled 状态、经 extreg
// 注册资产类型/策略/技能、刷新前端，这些注册表只存在于桌面进程。因此这里跑的就是
// 扩展页"从目录安装"按钮跑的那一个 extension_svc.Install，dev 与 prod 的加载路径
// 由构造相同，而不是靠两套宿主维持一致。
type ExtDevInstaller interface {
	// InstalledExtensionVersion 报告同名扩展是否已安装（启用或停用）及其版本，
	// 供确认弹窗说明这次安装会不会覆盖它。
	InstalledExtensionVersion(ctx context.Context, name string) (version string, installed bool)
	InstallExtensionDir(ctx context.Context, sourceDir string) (name, version string, err error)
}

// Opsctl binder。
type Opsctl struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider
	window WindowActivator

	approvalServer  *approval.Server
	authToken       string
	extExecutor     ExtToolExecutor
	extDevInstaller ExtDevInstaller
	// extDevApprove 把一次 ext dev 安装交给用户确认；New 接到 requestSingleApproval
	// （既有的 opsctl 审批弹窗），测试替换它以免触达 Wails 事件。
	extDevApprove   func(approval.ApprovalRequest) approval.ApprovalResponse
	extDevApprovals extDevApprovalMemory

	pendingOpsctlApprovals sync.Map // map[string]pendingOpsctlApproval
	mfa                    *mfaBroker
}

type pendingOpsctlApproval struct {
	kind  string
	items []permission.ApprovalItem // 后端保留原始 items，用于响应校验与执行
	ch    chan permission.ApprovalResponse
}

// SetAuthToken main.go 注入 socket 鉴权 token，供 startApprovalServer 使用。
func (o *Opsctl) SetAuthToken(token string) { o.authToken = token }

// SetExtToolExecutor main.go 注入扩展工具执行器。
func (o *Opsctl) SetExtToolExecutor(e ExtToolExecutor) { o.extExecutor = e }

// SetExtDevInstaller main.go 注入扩展开发安装器。
func (o *Opsctl) SetExtDevInstaller(i ExtDevInstaller) { o.extDevInstaller = i }

// New 构造 opsctl binder。
func New(
	appCtx context.Context,
	lang LangProvider,
	window WindowActivator,
) *Opsctl {
	o := &Opsctl{
		appCtx: appCtx,
		lang:   lang,
		window: window,
	}
	o.extDevApprove = o.requestSingleApproval
	return o
}

// Startup 启动审批的本地 IPC 服务。
func (o *Opsctl) Startup(ctx context.Context) {
	o.ctx = ctx
	o.mfa = newMFABroker(func(name string, payload map[string]any) {
		wailsRuntime.EventsEmit(o.ctx, name, payload)
	}, func() {
		if o.window != nil {
			o.window.ActivateWindow()
		}
	})
	o.startApprovalServer()
}

// Cleanup 关闭审批的本地 IPC 服务。
func (o *Opsctl) Cleanup() {
	if o.approvalServer != nil {
		o.approvalServer.Stop()
	}
}

// ActiveTaskCount returns the authenticated approval requests in flight, which
// an application shutdown would strand. opsctl dials its own connections, so a
// running remote command is not one of them and never blocks the quit prompt.
func (o *Opsctl) ActiveTaskCount() int {
	return activeApprovals(o)
}

// ActiveApprovals gives main.go the same count without widening the
// Wails-bound Opsctl method surface.
func ActiveApprovals(o *Opsctl) int { return activeApprovals(o) }

func activeApprovals(o *Opsctl) int {
	if o.approvalServer == nil {
		return 0
	}
	return o.approvalServer.ActiveRequests()
}
