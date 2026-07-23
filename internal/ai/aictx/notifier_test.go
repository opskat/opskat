package aictx

import "testing"

func TestNotifyDataChanged(t *testing.T) {
	// 全局单例，记得跑完恢复，避免污染其他测试。
	prev := dataChangeNotifier
	t.Cleanup(func() { dataChangeNotifier = prev })

	t.Run("notifier 未注入时静默忽略", func(t *testing.T) {
		dataChangeNotifier = nil
		NotifyDataChanged("asset")
	})
}
