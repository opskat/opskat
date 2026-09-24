// Package extension 实现 extension binder：扩展安装/启停、tool 调用、snippet 管理。
package extension

import (
	"context"

	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// PageToolGate runs one tool call an extension's own frontend page makes against
// the asset it was opened for, through the same policy check / in-app approval /
// grant / audit gate opsctl's socket bridge runs delegated exec commands through
// (main.go wires *opsctl.Opsctl in, whose RunPageToolCall implements this).
//
// Without this seam, CallExtensionTool would have to call straight into
// Plugin.CallTool — the direct-dial path opsctl.handleExtToolExec exists
// precisely to avoid, because it skips policy, approval, grant and audit
// entirely. command is the same `<tool> --flag=value` / `<tool> --json=<...>`
// exec DSL text the unified exec handler parses for every other caller (AI,
// opsctl). invocationID is the caller's per-call correlation token (not the
// grant session — a call on the same asset always uses the same session
// regardless of invocationID, so "always allow" survives past the one call that
// requested it). Canceling ctx — which CancelExtensionTool does — ends the call.
type PageToolGate interface {
	RunPageToolCall(ctx context.Context, invocationID string, assetID int64, command string) (string, error)
}

// Extension binder。
type Extension struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider
	pool   *sshpool.Pool

	service  *extension_svc.Service
	pageGate PageToolGate

	// toolCalls are the page tool calls in flight, for CancelExtensionTool.
	toolCalls toolCalls
}

// New 构造 extension binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *Extension {
	return &Extension{appCtx: appCtx, lang: lang, pool: pool}
}

// SetService main.go 在创建 extension_svc.Service 后注入。
func (e *Extension) SetService(svc *extension_svc.Service) { e.service = svc }

// SetPageToolGate main.go 注入页面工具调用闸门（*opsctl.Opsctl）。
func (e *Extension) SetPageToolGate(gate PageToolGate) { e.pageGate = gate }

// Service 返回当前持有的 extension_svc.Service（main.go 需要它去做附加配置）。
func (e *Extension) Service() *extension_svc.Service { return e.service }

// Pool 返回 SSH 池，供 main.go 构造 HostProvider 时使用。
func (e *Extension) Pool() *sshpool.Pool { return e.pool }

// Ctx 暴露 Wails ctx，给 HostProvider/dialog/event 用。
func (e *Extension) Ctx() context.Context { return e.ctx }

// NewHostProvider 由 main.go 调用：根据扩展名构造一个 HostProvider；与下文 ext 包内的 helpers 一起注入到 extension.NewManager 的 ProviderFactory。
func (e *Extension) NewHostProvider(extName string) interface{} {
	// 占位返回 nil interface；实际类型由 extension.NewDefaultHostProvider 决定。
	// main.go 直接调用 extension.NewDefaultHostProvider 时传入下文 helpers。
	return nil
}

// AssetConfigGetter / FileDialogOpener / KVStore / ActionEventHandler / AssetDialer 暴露给 main.go
// 作为 extension.NewDefaultHostProvider 的依赖。

// NewAssetConfigGetter 为指定扩展返回 assetConfigGetter：只服务该扩展自己注册的资产类型。
func (e *Extension) NewAssetConfigGetter(extName string) *assetConfigGetter {
	return &assetConfigGetter{ext: e, extName: extName}
}

// NewFileDialogOpener 返回 fileDialogOpener 实例。
func (e *Extension) NewFileDialogOpener() *fileDialogOpener { return &fileDialogOpener{ctx: e.ctx} }

// NewKVStore 为指定扩展返回 kvStore 实例。
func (e *Extension) NewKVStore(extName string) *kvStore { return &kvStore{ext: e, extName: extName} }

// NewActionEventHandler 为指定扩展返回 actionEventHandler 实例。
func (e *Extension) NewActionEventHandler(extName string) *actionEventHandler {
	return &actionEventHandler{ctx: e.ctx, extName: extName}
}

// NewAssetDialer 为指定扩展返回 assetDialer：解析该扩展资产声明的连接路径（SSH 隧道）。
func (e *Extension) NewAssetDialer(extName string) *assetDialer {
	return &assetDialer{ext: e, extName: extName}
}

// Startup 异步初始化扩展系统（WASM 编译较慢，单独协程跑）。
func (e *Extension) Startup(ctx context.Context) {
	e.ctx = ctx
}

// Cleanup 关闭扩展运行时。
func (e *Extension) Cleanup() {
	if e.service != nil {
		e.service.Close(context.Background())
	}
}
