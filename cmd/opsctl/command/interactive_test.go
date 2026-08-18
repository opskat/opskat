package command

import (
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// 可交互判据（spec Testing decisions：注入两个 isTerminal 结果的纯函数）：
// 仅当 stdin 与 stderr 双双是终端时才判为可交互；stdin 是管道（cat x | opsctl exec）
// 或 stderr 被重定向（opsctl exec ... 2>log）都归为非交互。
func TestIsInteractive(t *testing.T) {
	Convey("isInteractive：双 TTY 才算可交互", t, func() {
		So(isInteractive(true, true), ShouldBeTrue)
		So(isInteractive(true, false), ShouldBeFalse) // stderr 重定向 → 非交互
		So(isInteractive(false, true), ShouldBeFalse) // stdin 是管道 → 非交互
		So(isInteractive(false, false), ShouldBeFalse)
	})
}

// 审批人选择顺序（spec Testing decisions：注入可交互标志 + socket 拨号函数）：
// 可交互 → 终端提示且不发生拨号；不可交互且拨号成功 → 桌面弹窗；
// 不可交互且拨号失败（含 stale socket）→ 结构化拒绝。
func TestChooseApprover(t *testing.T) {
	Convey("chooseApprover 三条路径", t, func() {
		Convey("可交互走终端，且不拨 approval.sock", func() {
			dialed := 0
			dial := func() error {
				dialed++
				return nil
			}
			So(chooseApprover(true, dial), ShouldEqual, approverTerminal)
			So(dialed, ShouldEqual, 0)
		})

		Convey("不可交互且 socket 可达走桌面弹窗", func() {
			So(chooseApprover(false, func() error { return nil }), ShouldEqual, approverDesktop)
		})

		Convey("不可交互且 socket 不可达走结构化拒绝", func() {
			So(chooseApprover(false, func() error { return errors.New("connection refused") }),
				ShouldEqual, approverRefusal)
		})
	})
}
