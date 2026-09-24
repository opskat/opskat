package quit

import "testing"

type closeSpy struct {
	shown      []Activity
	showCalled bool
	showAck    bool
	flushed    bool
}

func (s *closeSpy) prompt() Prompt {
	return Prompt{
		Show: func(activities []Activity) bool {
			s.showCalled = true
			s.shown = activities
			return s.showAck
		},
		FlushAI: func() { s.flushed = true },
	}
}

func running() Activity    { return Activity{Kind: "ai", Category: "running", RefID: 7} }
func connection() Activity { return Activity{Kind: "terminal", Category: "connection", Title: "web-1"} }

// 空闲的远程连接关掉不丢任何东西，不该把用户拦在退出流程里。
func TestOnBeforeCloseAllowsQuitWithOnlyConnections(t *testing.T) {
	spy := &closeSpy{}

	if OnBeforeClose(false, []Activity{connection(), connection()}, spy.prompt()) {
		t.Fatal("open connections blocked quit, want quit allowed")
	}
	if spy.showCalled {
		t.Fatal("confirm dialog shown for connections only")
	}
	if !spy.flushed {
		t.Fatal("AI conversations not flushed before quitting")
	}
}

func TestOnBeforeCloseBlocksOnRunningTask(t *testing.T) {
	spy := &closeSpy{showAck: true}

	if !OnBeforeClose(false, []Activity{connection(), running()}, spy.prompt()) {
		t.Fatal("running task did not block quit")
	}
	if len(spy.shown) != 2 {
		t.Fatalf("dialog got %d activities, want both the running task and the connection as context", len(spy.shown))
	}
	if spy.flushed {
		t.Fatal("flushed AI while the quit was still blocked awaiting confirmation")
	}
}

// 用户已经在对话框里确认过中断，此时仍必须落盘：确认退出恰恰发生在 AI 流式
// 输出进行中，跳过 flush 会丢掉这一轮的增量。
func TestOnBeforeCloseFlushesAfterUserConfirmedInterruption(t *testing.T) {
	spy := &closeSpy{showAck: true}

	if OnBeforeClose(true, []Activity{running()}, spy.prompt()) {
		t.Fatal("confirmed quit was blocked again")
	}
	if spy.showCalled {
		t.Fatal("confirm dialog shown again after the user already confirmed")
	}
	if !spy.flushed {
		t.Fatal("AI conversations not flushed on the confirmed-quit path")
	}
}

// 前端没回执说明它已经显示不了对话框（webview 卡死/事件丢失）。此时继续阻挡
// 会让窗口永远关不掉，只能杀进程。
func TestOnBeforeCloseQuitsWhenFrontendCannotShowDialog(t *testing.T) {
	spy := &closeSpy{showAck: false}

	if OnBeforeClose(false, []Activity{running()}, spy.prompt()) {
		t.Fatal("quit stayed blocked although the frontend never acknowledged the dialog")
	}
	if !spy.flushed {
		t.Fatal("AI conversations not flushed on the unresponsive-frontend path")
	}
}
