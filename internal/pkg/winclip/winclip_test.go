//go:build windows

package winclip

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procEmptyClipboard          = modUser32.NewProc("EmptyClipboard")
	procSetClipboardData        = modUser32.NewProc("SetClipboardData")
	procRegisterClipboardFormat = modUser32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc             = modKernel32.NewProc("GlobalAlloc")
)

// putClipboard replaces the clipboard contents with a single format holding data.
func putClipboard(t *testing.T, format uintptr, data []byte) {
	t.Helper()
	err := Do(func() error {
		if r, _, err := procEmptyClipboard.Call(); r == 0 {
			return err
		}
		if len(data) == 0 {
			return nil
		}
		hMem, _, err := procGlobalAlloc.Call(0x0002 /* GMEM_MOVEABLE */, uintptr(len(data)))
		if hMem == 0 {
			return err
		}
		ptr, _, err := procGlobalLock.Call(hMem)
		if ptr == 0 {
			return err
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data)), data)
		_, _, _ = procGlobalUnlock.Call(hMem)
		if r, _, err := procSetClipboardData.Call(format, hMem); r == 0 {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("put clipboard: %v", err)
	}
}

func putText(t *testing.T, s string) {
	t.Helper()
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		t.Fatal(err)
	}
	putClipboard(t, cfUnicodeText, unsafe.Slice((*byte)(unsafe.Pointer(&u[0])), len(u)*2))
}

func TestReadText_EmptyClipboardIsNoText(t *testing.T) {
	putClipboard(t, 0, nil)

	text, err := ReadText()
	if err != nil {
		t.Fatalf("empty clipboard must not be an error, got %v", err)
	}
	if text != "" {
		t.Fatalf("want empty text, got %q", text)
	}
}

// A screenshot or image copy leaves only non-text formats on the clipboard; that is
// "nothing to paste", not a failure (issue #367).
func TestReadText_NonTextOnlyIsNoText(t *testing.T) {
	name, _ := syscall.UTF16PtrFromString("OpsKatWinclipTestImage")
	format, _, err := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
	if format == 0 {
		t.Fatalf("RegisterClipboardFormatW: %v", err)
	}
	putClipboard(t, format, []byte("\x89PNG fake image"))

	text, err := ReadText()
	if err != nil {
		t.Fatalf("non-text clipboard must not be an error, got %v", err)
	}
	if text != "" {
		t.Fatalf("want empty text, got %q", text)
	}
}

func TestReadText_ReturnsUnicodeText(t *testing.T) {
	want := "你好 clipboard 🙂\r\nline2"
	putText(t, want)

	got, err := ReadText()
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

// Text larger than 1<<20 UTF-16 units must come back whole.
func TestReadText_LargeTextNotTruncated(t *testing.T) {
	big := make([]rune, 1<<20+1234)
	for i := range big {
		big[i] = 'a' + rune(i%26)
	}
	want := string(big)
	putText(t, want)

	got, err := ReadText()
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if len(got) != len(want) || got != want {
		t.Fatalf("want %d bytes, got %d", len(want), len(got))
	}
}

// The clipboard is exclusive across processes, so the cases below run the test binary
// itself as a second process (selected by helperEnv). The helper opens it with a real
// window, as clipboard managers and remote-desktop clients do: a NULL-owner open by one
// thread or process doesn't keep a NULL-owner open by another out, so it can't model this.
const helperEnv = "WINCLIP_TEST_HELPER"

var procCreateWindowEx = modUser32.NewProc("CreateWindowExW")

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("helper process only")
	}
	runtime.LockOSThread()
	class, _ := syscall.UTF16PtrFromString("STATIC")
	const hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3): message-only window
	hwnd, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if hwnd == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "CreateWindowExW failed: %v\n", err)
		os.Exit(1)
	}
	r, _, err := procOpenClipboard.Call(hwnd)
	switch mode {
	case "hold":
		if r == 0 {
			_, _ = fmt.Fprintf(os.Stdout, "open failed: %v\n", err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintln(os.Stdout, "held")
		time.Sleep(300 * time.Millisecond)
		_, _, _ = procCloseClipboard.Call()
	case "probe":
		if r == 0 {
			_, _ = fmt.Fprintf(os.Stdout, "open failed: %v\n", err)
			os.Exit(1)
		}
		_, _, _ = procCloseClipboard.Call()
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	return cmd
}

// Another process holding the clipboard open (clipboard managers, remote-desktop
// tools) makes OpenClipboard fail briefly; Do must wait for it rather than error out.
func TestDo_WaitsWhileAnotherProcessHoldsClipboard(t *testing.T) {
	putText(t, "held")

	cmd := helper(t, "hold")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "held" {
		_ = cmd.Wait()
		t.Fatalf("helper did not take the clipboard: %q %v", line, err)
	}

	got, readErr := ReadText()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	if readErr != nil {
		t.Fatalf("ReadText while clipboard briefly held by another process: %v", readErr)
	}
	if got != "held" {
		t.Fatalf("want %q, got %q", "held", got)
	}
}

// Do closes the clipboard even when fn fails, so other processes can open it.
func TestDo_ReleasesClipboardAfterError(t *testing.T) {
	sentinel := windows.ERROR_INVALID_DATA
	if err := Do(func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("want fn error passed through, got %v", err)
	}

	if out, err := helper(t, "probe").CombinedOutput(); err != nil {
		t.Fatalf("clipboard still held after Do returned: %v %s", err, out)
	}
}
