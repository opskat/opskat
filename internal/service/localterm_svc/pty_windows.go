//go:build windows

package localterm_svc

import (
	"os"
	"os/exec"

	"github.com/UserExistsError/conpty"
)

type winPTY struct {
	cpty *conpty.ConPty
}

// windowsDefaultShell 按 pwsh → powershell → cmd 兜底。
func windowsDefaultShell() string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if c := os.Getenv("COMSPEC"); c != "" {
		return c
	}
	return "cmd.exe"
}

func startPTY(spec ptySpec) (ptyProcess, error) {
	if !conpty.IsConPtyAvailable() {
		return nil, conpty.ErrConPtyUnsupported
	}
	shell := spec.Shell
	if shell == "" {
		shell = windowsDefaultShell()
	}
	cmdline := windowsCommandLine(shell, spec.Args)
	cwd, err := expandHomeDir(spec.Cwd)
	if err != nil {
		return nil, err
	}

	cols, rows := clampSize(spec.Cols, spec.Rows)
	opts := []conpty.ConPtyOption{conpty.ConPtyDimensions(cols, rows)}
	opts = append(opts, conpty.ConPtyWorkDir(cwd))
	cpty, err := conpty.Start(cmdline, opts...)
	if err != nil {
		return nil, err
	}
	return &winPTY{cpty: cpty}, nil
}

func (p *winPTY) Read(b []byte) (int, error)  { return p.cpty.Read(b) }
func (p *winPTY) Write(b []byte) (int, error) { return p.cpty.Write(b) }

func (p *winPTY) Resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	return p.cpty.Resize(cols, rows)
}

func (p *winPTY) Close() error {
	// conpty@v0.1.4 的 ConPty.Close() 同步完成全部回收:
	// ClosePseudoConsole 关闭伪控制台并杀掉挂接的子进程,随后 closeHandles
	// 关闭进程/线程句柄与全部管道句柄。因此一次同步 Close 即可——既能立刻
	// 关闭伪控制台触发子进程退出(断开活动会话无多秒延迟),又不残留句柄。
	// 注意:不能再 go Wait,Wait 轮询 WaitForSingleObject(pi.Process),
	// 而该进程句柄正是 Close 要关掉的,二者并发会触发句柄 use-after-close。
	return p.cpty.Close()
}
