package system

import (
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// bootShowTimeout 是窗口隐藏启动后等前端报告加载界面就绪的上限。超时说明网页没能
// 加载（或内联脚本没跑起来），这时照样显示窗口：宁可露出一个未就绪的窗口，也不能让
// 应用成为一个看不见的进程。
const bootShowTimeout = 10 * time.Second

// BootScreenShown 由 index.html 里的内联脚本在加载界面进入 DOM 后调用。窗口以隐藏状态
// 启动（main.go 的 StartHidden），在这里才显示：用户看到的第一帧就是加载界面，而不是
// 窗口出现到网页请求之间那 0.3~2 秒的空窗口。只有第一次调用生效。
func (s *System) BootScreenShown() {
	s.bootShow.Do(func() {
		logger.Ctx(s.ctx).Info("boot screen ready, showing window")
		windowOps.Show(s.ctx)
	})
}

// startBootShowTimeout 在 d 之后仍未收到 BootScreenShown 时直接显示窗口。
func (s *System) startBootShowTimeout(d time.Duration) {
	time.AfterFunc(d, func() {
		s.bootShow.Do(func() {
			logger.Ctx(s.ctx).Warn("boot screen not reported in time, showing window anyway", zap.Duration("timeout", d))
			windowOps.Show(s.ctx)
		})
	})
}
