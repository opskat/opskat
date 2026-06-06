// Package system 实现 system binder：设置、凭证、资产 CRUD、状态、字体、重启等控制面方法。
//
// 同时承载所有 binder 共用的 LangProvider/WindowActivator 接口实现：
// 语言状态作为唯一可信源在 system 里，其它 binder 持有 *system.System 通过 Lang() 读取。
package system

import (
	"context"
	"sync"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// SkillContent 内嵌的 skill/plugin 文件内容（由 main.go 通过 go:embed 注入）
type SkillContent struct {
	SkillMD               string
	CommandsMD            string
	InitMD                string
	PluginJSON            string
	MarketplaceJSON       string
	PluginMarketplaceJSON string
}

// System 控制面 binder：设置、凭证、资产、状态、重启、字体等。
// 同时是 LangProvider / WindowActivator 实现。
type System struct {
	appCtx       context.Context
	ctx          context.Context
	skillContent SkillContent

	mu   sync.RWMutex
	lang string

	githubAuthCancel context.CancelFunc
}

type windowRuntimeOps struct {
	IsMinimised    func(context.Context) bool
	Unminimise     func(context.Context)
	Show           func(context.Context)
	SetAlwaysOnTop func(context.Context, bool)
}

var windowOps = windowRuntimeOps{
	IsMinimised:    wailsRuntime.WindowIsMinimised,
	Unminimise:     wailsRuntime.WindowUnminimise,
	Show:           wailsRuntime.WindowShow,
	SetAlwaysOnTop: wailsRuntime.WindowSetAlwaysOnTop,
}

// New 构造 System binder。appCtx 来自 main.go 的根 context（cancel 后所有 binder 退出）。
func New(appCtx context.Context, skill SkillContent) *System {
	return &System{
		appCtx:       appCtx,
		skillContent: skill,
		lang:         "zh-cn",
	}
}

// Startup Wails 启动回调：保存 Wails ctx 后续 EventsEmit 用，并触发自动更新检查、emit 系统状态。
func (s *System) Startup(ctx context.Context) {
	s.ctx = ctx
	s.startAutoUpdateCheck()
	s.emitSystemStatusImpl()
}

// Cleanup 关闭时调用：当前没有持有的资源。
func (s *System) Cleanup() {}

// Lang 返回当前语言（LangProvider 接口）。
func (s *System) Lang() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lang
}

// SetLanguage 前端调用，同步语言设置。
func (s *System) SetLanguage(lang string) {
	s.mu.Lock()
	s.lang = lang
	s.mu.Unlock()
}

// GetLanguage 返回当前语言。
func (s *System) GetLanguage() string {
	return s.Lang()
}

// ActivateWindow 把窗口拉到前台（WindowActivator 接口；审批弹窗等场景调用）。
func (s *System) ActivateWindow() {
	if s.ctx == nil {
		return
	}
	if windowOps.IsMinimised(s.ctx) {
		windowOps.Unminimise(s.ctx)
	}
	windowOps.Show(s.ctx)
	windowOps.SetAlwaysOnTop(s.ctx, true)
	windowOps.SetAlwaysOnTop(s.ctx, false)
}

// OnSecondInstanceLaunch 第二个实例启动时激活当前窗口。
func (s *System) OnSecondInstanceLaunch() {
	s.ActivateWindow()
}
