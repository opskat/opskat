//go:build windows

package localterm_svc

import (
	"context"
	"os"
	"os/exec"
	"strings"

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
	// Windows 命令行是单字符串;v1 用空格拼接(shell 路径/参数一般无空格,含空格场景后续再加引号处理)。
	cmdline := shell
	if len(spec.Args) > 0 {
		cmdline = shell + " " + strings.Join(spec.Args, " ")
	}

	cols, rows := clampSize(spec.Cols, spec.Rows)
	opts := []conpty.ConPtyOption{conpty.ConPtyDimensions(cols, rows)}
	if spec.Cwd != "" {
		opts = append(opts, conpty.ConPtyWorkDir(spec.Cwd))
	}
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
	// conpty.Close 关闭伪控制台句柄并触发子进程退出;Wait 回收。
	go func() { _, _ = p.cpty.Wait(context.Background()) }()
	return p.cpty.Close()
}
