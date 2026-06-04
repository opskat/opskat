//go:build !windows

package localterm_svc

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func startPTY(spec ptySpec) (ptyProcess, error) {
	shell := spec.Shell
	if shell == "" {
		shell = defaultShell()
	}
	cwd, err := expandHomeDir(spec.Cwd)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(shell, spec.Args...) //nolint:gosec // G204: 启动用户在本地终端资产里选择的 shell 是本功能的核心意图
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	cols, rows := clampSize(spec.Cols, spec.Rows)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixPTY) Close() error {
	// 先给前台进程组发 SIGHUP 触发 shell 退出,再关 PTY master。
	_ = p.cmd.Process.Signal(syscall.SIGHUP)
	err := p.f.Close()
	// 后台带超时回收,避免僵尸进程,也避免 Close 阻塞调用方。
	go func() {
		done := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
	}()
	return err
}
