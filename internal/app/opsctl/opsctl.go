// Package opsctl 实现 opsctl binder：对 opsctl CLI 暴露的 Unix socket 桥（审批 + 资产 + SSH 池代理）。
//
// 只有一个 Wails 绑定方法（RespondOpsctlApproval）；其它都是底层服务。
package opsctl

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/sshpool"
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
	InstallExtensionDir(ctx context.Context, sourceDir string) (name, version string, err error)
}

// Opsctl binder。
type Opsctl struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider
	window WindowActivator

	approvalServer  *approval.Server
	proxyServer     *sshpool.Server
	authToken       string
	extExecutor     ExtToolExecutor
	extDevInstaller ExtDevInstaller

	pendingOpsctlApprovals sync.Map // map[string]pendingOpsctlApproval
}

type pendingOpsctlApproval struct {
	kind  string
	items []permission.ApprovalItem // 后端保留原始 items，用于响应校验与执行
	ch    chan permission.ApprovalResponse
}

// SetAuthToken main.go 注入 socket 鉴权 token，供 startApprovalServer/startSSHPoolServer 使用。
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
	proxySrv *sshpool.Server,
) *Opsctl {
	return &Opsctl{
		appCtx:      appCtx,
		lang:        lang,
		window:      window,
		proxyServer: proxySrv,
	}
}

// Startup 启动 Unix socket 服务（审批 + SSH 代理）。
func (o *Opsctl) Startup(ctx context.Context) {
	o.ctx = ctx
	o.startApprovalServer()
	o.startSSHPoolServer()
}

// Cleanup 关闭两个 Unix socket 服务。
func (o *Opsctl) Cleanup() {
	if o.proxyServer != nil {
		o.proxyServer.Stop()
	}
	if o.approvalServer != nil {
		o.approvalServer.Stop()
	}
}

// ActiveTaskCount returns opsctl operations that have started and would be
// interrupted by an application shutdown. Idle and half-open connections are
// deliberately excluded.
func (o *Opsctl) ActiveTaskCount() int {
	return len(activeTasks(o))
}

// ActiveTasks returns one stable kind per authenticated request without
// widening the Wails-bound Opsctl method surface.
func ActiveTasks(o *Opsctl) []string { return activeTasks(o) }

func activeTasks(o *Opsctl) []string {
	tasks := make([]string, 0)
	count := 0
	if o.proxyServer != nil {
		count = o.proxyServer.ActiveRequests()
		for range count {
			tasks = append(tasks, "operation")
		}
	}
	if o.approvalServer != nil {
		count = o.approvalServer.ActiveRequests()
		for range count {
			tasks = append(tasks, "approval")
		}
	}
	return tasks
}
