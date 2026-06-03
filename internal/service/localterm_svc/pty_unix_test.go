//go:build !windows

package localterm_svc

import (
	"bufio"
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

func TestStartPTYResizeNoError(t *testing.T) {
	proc, err := startPTY(ptySpec{Shell: "/bin/sh"})
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()
	require.NoError(t, proc.Resize(120, 40))
}

// readerFunc 把 proc.Read 适配成 io.Reader。
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
