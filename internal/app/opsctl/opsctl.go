// Package opsctl 实现 opsctl binder：对 opsctl CLI 暴露的 Unix socket 桥（审批 + 资产 + SSH 池代理）。
//
// 只有一个 Wails 绑定方法（RespondOpsctlApproval）；其它都是底层服务。
package opsctl

import (
	"context"
	"sync"

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

// ExtToolExecutor 在 opsctl Unix socket 收到 ext_tool 请求时回调到 ai/extension binder。
// 由 main.go 注入：通常实现是 extension binder 的 service.Bridge().CallTool 包装。
type ExtToolExecutor interface {
	ExecuteExtTool(ctx context.Context, extName, tool string, args []byte) ([]byte, error)
}

// Opsctl binder。
type Opsctl struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider
	window WindowActivator

	approvalServer *approval.Server
	proxyServer    *sshpool.Server
	authToken      string
	extExecutor    ExtToolExecutor

	pendingOpsctlApprovals sync.Map // map[string]chan ai.ApprovalResponse
}

// SetAuthToken main.go 注入 socket 鉴权 token，供 startApprovalServer/startSSHPoolServer 使用。
func (o *Opsctl) SetAuthToken(token string) { o.authToken = token }

// SetExtToolExecutor main.go 注入扩展工具执行器。
func (o *Opsctl) SetExtToolExecutor(e ExtToolExecutor) { o.extExecutor = e }

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
