package quit

// Prompt 是 OnBeforeClose 需要的两个副作用，由 main.go 用 Wails runtime 提供。
type Prompt struct {
	// Show 请求前端弹出退出确认框，返回前端是否回执"已接手"。
	Show func(activities []Activity) bool
	// FlushAI 把进行中的 AI 会话落盘。
	FlushAI func()
}

// OnBeforeClose 决定窗口关闭是否要被拦下。
//
// 只有"运行中"的活动（AI 生成、opsctl 任务）才值得拦：它们被打断就丢进度。
// 空闲的远程连接（终端 / RDP / VNC）关掉不丢任何东西，只在确认框里作为上下文
// 一并展示，本身不构成阻挡理由。
//
// 无论走哪条放行路径都要 FlushAI —— 用户确认中断（forced）时 AI 往往正在流式
// 输出，恰恰是最需要落盘的时刻。前端没能接手对话框时同样放行，否则窗口会永远
// 关不掉。
func OnBeforeClose(forced bool, activities []Activity, prompt Prompt) bool {
	if !forced && hasInterruptibleWork(activities) && prompt.Show(activities) {
		return true
	}
	prompt.FlushAI()
	return false
}

func hasInterruptibleWork(activities []Activity) bool {
	for _, activity := range activities {
		if activity.Category == "running" {
			return true
		}
	}
	return false
}
