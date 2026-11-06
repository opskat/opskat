//go:build windows

package helper

import (
	"os"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

var (
	kernel32                        = windows.NewLazySystemDLL("kernel32.dll")
	procCreateConsoleScreenBuffer   = kernel32.NewProc("CreateConsoleScreenBuffer")
	procReadConsoleOutputCharacterW = kernel32.NewProc("ReadConsoleOutputCharacterW")
	procAllocConsole                = kernel32.NewProc("AllocConsole")
	procFreeConsole                 = kernel32.NewProc("FreeConsole")
)

const consoleTextModeBuffer = 1 // CONSOLE_TEXTMODE_BUFFER

// newConsoleScreenBuffer 在测试进程所连的控制台上新建一块屏幕缓冲区，作为子进程的
// stdout：opsctl 在终端里运行时 stdout 就是这样一个控制台句柄（而不是管道）。测试进程
// 没连控制台时（例如被不带 pty 的 ssh 启动）先 AllocConsole 一个。
func newConsoleScreenBuffer(t *testing.T) *os.File {
	t.Helper()
	create := func() (uintptr, error) {
		h, _, err := procCreateConsoleScreenBuffer.Call(
			uintptr(windows.GENERIC_READ|windows.GENERIC_WRITE),
			uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE),
			0, consoleTextModeBuffer, 0)
		if windows.Handle(h) == windows.InvalidHandle {
			return 0, err
		}
		return h, nil
	}
	h, err := create()
	if err != nil {
		r, _, allocErr := procAllocConsole.Call()
		require.NotZero(t, r, "AllocConsole: %v", allocErr)
		t.Cleanup(func() { _, _, _ = procFreeConsole.Call() })
		h, err = create()
	}
	require.NoError(t, err, "CreateConsoleScreenBuffer")
	f := os.NewFile(h, "console-screen-buffer")
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// readConsoleText 读回屏幕缓冲区前 rows 行的字符（子进程从 (0,0) 开始写）。
func readConsoleText(t *testing.T, buf *os.File, rows int) string {
	t.Helper()
	var info windows.ConsoleScreenBufferInfo
	require.NoError(t, windows.GetConsoleScreenBufferInfo(windows.Handle(buf.Fd()), &info))
	n := int(info.Size.X) * rows
	chars := make([]uint16, n)
	var read uint32
	r, _, err := procReadConsoleOutputCharacterW.Call(buf.Fd(),
		uintptr(unsafe.Pointer(&chars[0])), uintptr(n), 0, uintptr(unsafe.Pointer(&read))) //nolint:gosec // Win32 out-params: buffers live for the duration of the call
	require.NotZero(t, r, "ReadConsoleOutputCharacterW: %v", err)
	return windows.UTF16ToString(chars[:read])
}

// opsctl exec 在终端里运行（例如终端审批之后）时，stdout 是控制台句柄。通用资产的本地
// 命令必须把输出写进这个控制台，而不是写进一个看不见的控制台——退出码透传之外，输出也要
// 透传（spec「本地命令」：stdin、stdout、stderr 直接透传）。
func TestGenericCommandStream_ChildOutputReachesCallersConsole(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "wincmd", Slug: "wincmd", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "region"}},
		Command: &custom_type_entity.CommandConfig{Template: "cmd /c"},
	}))
	asset := genericAsset(t, "wincmd", map[string]string{"region": "r1"})
	console := newConsoleScreenBuffer(t)

	res, err := StreamGenericOnAsset(ctx, asset,
		[]string{"echo", "w1-console-marker&", "exit", "/b", "5"},
		permission.Stdio{Stdout: console, Stderr: console})
	require.NoError(t, err)
	assert.Equal(t, 5, res.ExitCode, "exit code is passed through")

	text := readConsoleText(t, console, 5)
	assert.Contains(t, strings.TrimSpace(text), "w1-console-marker",
		"child output must land in the caller's console, not in a hidden console of its own")
}
