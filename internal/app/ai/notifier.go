package ai

import (
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// dataChangeNotifier 实现 aictx.DataChangeNotifier，把 AI 触发的资产/分组变更
// 通过 Wails 的 data:changed 事件广播给前端，与 opsctl Unix socket 事件复用同一前端监听器。
type dataChangeNotifier struct {
	ai *AI
}

func (n *dataChangeNotifier) NotifyDataChanged(resource string) {
	if n.ai == nil || n.ai.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(n.ai.ctx, "data:changed", map[string]any{
		"resource": resource,
	})
}
