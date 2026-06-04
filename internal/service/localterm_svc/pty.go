// Package localterm_svc 管理本地 shell(PTY)终端会话。
package localterm_svc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ptyProcess 是一个本地 shell 进程 + 其伪终端(PTY)的最小抽象。
// 平台实现见 pty_unix.go(creack/pty)与 pty_windows.go(conpty)。
type ptyProcess interface {
	Read(p []byte) (int, error)  // 读 PTY 输出(stdout+stderr 合流);进程退出后返回 EOF
	Write(p []byte) (int, error) // 写 PTY 输入(用户键入)
	Resize(cols, rows int) error // 调整窗口尺寸
	Close() error                // 关闭 PTY 并回收子进程(实现内部带超时,避免僵尸)
}

// ptySpec 描述要启动的本地 shell。
type ptySpec struct {
	Shell string   // 为空时由平台实现按 OS 兜底
	Args  []string // shell 参数,如 ["-d","Ubuntu"]
	Cwd   string   // 工作目录,空或 "~" 则展开为用户 home
	Cols  int      // 初始列(<=0 取 80)
	Rows  int      // 初始行(<=0 取 24)
}

// startPTYFn 是 startPTY 的可替换入口,测试用 fake 注入。
var startPTYFn = startPTY

func clampSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return cols, rows
}

func expandHomeDir(path string) (string, error) {
	if path == "" {
		path = "~"
	}
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func windowsCommandLine(shell string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, windowsQuoteArg(shell))
	for _, arg := range args {
		parts = append(parts, windowsQuoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func windowsQuoteArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\n\v\r\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat("\\", backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat("\\", backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat("\\", backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
