//go:build !windows

package localterm_svc

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartPTYEchoesInput(t *testing.T) {
	proc, err := startPTY(ptySpec{Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()

	_, err = proc.Write([]byte("echo hello-pty\n"))
	require.NoError(t, err)

	// 读输出直到看到 echo 结果或超时。
	// 缓冲容量 2:超时分支 t.Fatal 后没人再收,读 goroutine 仍可无阻塞地
	// 投递结果后退出,避免 goroutine 泄漏。
	out := make(chan string, 2)
	go func() {
		r := bufio.NewReader(readerFunc(proc.Read))
		var sb strings.Builder
		buf := make([]byte, 1024)
		for {
			n, e := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), "hello-pty") {
					out <- sb.String()
					return
				}
			}
			if e != nil {
				out <- sb.String()
				return
			}
		}
	}()

	select {
	case s := <-out:
		require.Contains(t, s, "hello-pty")
	case <-time.After(5 * time.Second):
		t.Fatal("PTY 未在超时内回显输入")
	}
}

// TestStartPTYExportsTruecolorEnv 验证本地 PTY 会给 shell 注入 COLORTERM=truecolor,
// 这样只按 COLORTERM 判断是否发 24-bit SGR 的 TUI 程序也能输出真彩,而不是被
// TERM=xterm-256color 的 256 色能力位限制住。
func TestStartPTYExportsTruecolorEnv(t *testing.T) {
	proc, err := startPTY(ptySpec{Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()

	_, err = proc.Write([]byte("printenv COLORTERM\n"))
	require.NoError(t, err)

	out := make(chan string, 2)
	go func() {
		r := bufio.NewReader(readerFunc(proc.Read))
		var sb strings.Builder
		buf := make([]byte, 1024)
		for {
			n, e := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), "truecolor") {
					out <- sb.String()
					return
				}
			}
			if e != nil {
				out <- sb.String()
				return
			}
		}
	}()

	select {
	case s := <-out:
		require.Contains(t, s, "truecolor")
	case <-time.After(5 * time.Second):
		t.Fatal("PTY 未在超时内输出 COLORTERM=truecolor")
	}
}

func TestStartPTYResizeNoError(t *testing.T) {
	proc, err := startPTY(ptySpec{Shell: "/bin/sh"})
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()
	require.NoError(t, proc.Resize(120, 40))
}

func TestStartPTYUsesHomeDirCwd(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)

	for _, tt := range []struct {
		name string
		cwd  string
	}{
		{name: "explicit tilde", cwd: "~"},
		{name: "empty default", cwd: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			proc, err := startPTY(ptySpec{Shell: "/bin/sh", Cwd: tt.cwd})
			require.NoError(t, err)
			defer func() { _ = proc.Close() }()

			_, err = proc.Write([]byte("pwd\n"))
			require.NoError(t, err)

			out := make(chan string, 2)
			go func() {
				r := bufio.NewReader(readerFunc(proc.Read))
				var sb strings.Builder
				buf := make([]byte, 1024)
				for {
					n, e := r.Read(buf)
					if n > 0 {
						sb.Write(buf[:n])
						if strings.Contains(sb.String(), home) {
							out <- sb.String()
							return
						}
					}
					if e != nil {
						out <- sb.String()
						return
					}
				}
			}()

			select {
			case s := <-out:
				require.Contains(t, s, home)
			case <-time.After(5 * time.Second):
				t.Fatal("PTY 未在超时内输出 home 目录")
			}
		})
	}
}

// TestManagerSplitFromSpawnsRealWorkingShell 走真实 PTY 验证本地分屏:从一个真实
// /bin/sh 会话 SplitFrom 出第二个会话,向新会话写入命令,确认它独立回显 —— 即分屏
// 出的是一个全新的、可交互的 shell,而非复用原 PTY。
func TestManagerSplitFromSpawnsRealWorkingShell(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(mgr.CloseAll)

	sid1, err := mgr.Connect(ConnectConfig{Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	mgr.SetCallbacks(sid1, func([]byte) {}, nil)

	sid2, err := mgr.SplitFrom(sid1, 80, 24)
	require.NoError(t, err)
	require.NotEqual(t, sid1, sid2, "分屏应得到一个新会话")

	out := make(chan []byte, 256)
	mgr.SetCallbacks(sid2, func(b []byte) {
		cp := make([]byte, len(b))
		copy(cp, b)
		out <- cp
	}, nil)

	sess2, ok := mgr.GetSession(sid2)
	require.True(t, ok)
	require.NoError(t, sess2.Write([]byte("echo hello-split\n")))

	deadline := time.After(5 * time.Second)
	var sb strings.Builder
	for {
		select {
		case b := <-out:
			sb.Write(b)
			if strings.Contains(sb.String(), "hello-split") {
				return // 分屏出的 shell 真正回显了输入 → 是独立可用的 shell
			}
		case <-deadline:
			t.Fatalf("分屏出的 shell 未在超时内回显输入,已收到: %q", sb.String())
		}
	}
}

// readerFunc 把 proc.Read 适配成 io.Reader。
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
