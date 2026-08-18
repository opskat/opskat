package command

import (
	"net"
	"os"

	"golang.org/x/term"
)

// 可交互判据（spec Interactivity criterion）：仅当 stdin 与 stderr 双双是终端时，
// 一次需要审批的操作才算可交互。判据不受任何命令行开关影响——不提供强制交互的
// 开关（无 TTY 时读 stdin 只会立刻 EOF，等同拒绝），也不提供强制非交互的开关
// （非交互本来就是默认的降级方向）。stdin 是管道的合法人工用法（cat x | opsctl exec）
// 因此归入非交互，需要事先 opsctl policy allow 授权。
//
// 做成注入两个 isTerminal 结果的纯函数，测试不依赖真实 TTY（spec Testing decisions）。
func isInteractive(stdinTTY, stderrTTY bool) bool {
	return stdinTTY && stderrTTY
}

// stdinIsTerminal / stderrIsTerminal 是两个终端探测的生产实现，变量化让
// requireApproval 级测试能强制交互/非交互（交互式终端里跑 go test 也不受真实 TTY 干扰）。
var (
	stdinIsTerminal  = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	stderrIsTerminal = func() bool { return term.IsTerminal(int(os.Stderr.Fd())) }
)

// approverChoice 是 NeedConfirm 之后审批人的三种去向（spec Approver selection）。
type approverChoice int

const (
	// approverTerminal：可交互 → 终端提示，不联系桌面端。
	approverTerminal approverChoice = iota
	// approverDesktop：不可交互且 approval.sock 可达 → 桌面弹窗（含 stale socket 判定）。
	approverDesktop
	// approverRefusal：不可交互且不可达 → 结构化拒绝（退出码 3 + 固定标记）。
	approverRefusal
)

// chooseApprover 按顺序选择审批人：可交互 → 终端（此时不发生拨号）；不可交互时拨
// approval.sock，拨号失败（含 stale socket 文件）即视为不可达 → 结构化拒绝。
// 注入可交互标志与 socket 拨号函数，三条路径可以各自单测（spec Testing decisions）。
func chooseApprover(interactive bool, dial func() error) approverChoice {
	if interactive {
		return approverTerminal
	}
	if dial() == nil {
		return approverDesktop
	}
	return approverRefusal
}

// dialApprovalSocket 探测 approval.sock 是否可达。变量化是为了 requireApproval 级
// 测试注入，避免连到真实数据目录下的桌面端审批 socket。
var dialApprovalSocket = func(path string) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	return conn.Close()
}
