//go:build windows

package rdp_svc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/opskat/opskat/internal/pkg/winclip"
)

// Win32 file clipboard via CF_HDROP, replacing a powershell.exe subprocess so the
// feature no longer depends on PowerShell being present or spawnable. Procs are
// resolved lazily from System32 to avoid DLL search-path hijacking; opening and
// closing the clipboard goes through winclip.Do.
const (
	cfHDROP      = 15     // CF_HDROP clipboard format
	gmemMoveable = 0x0002 // GMEM_MOVEABLE
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procEmptyClipboard   = modUser32.NewProc("EmptyClipboard")
	procGetClipboardData = modUser32.NewProc("GetClipboardData")
	procSetClipboardData = modUser32.NewProc("SetClipboardData")

	procDragQueryFileW = modShell32.NewProc("DragQueryFileW")

	procGlobalAlloc  = modKernel32.NewProc("GlobalAlloc")
	procGlobalFree   = modKernel32.NewProc("GlobalFree")
	procGlobalLock   = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock = modKernel32.NewProc("GlobalUnlock")
)

func readLocalClipboardFilePaths() ([]string, error) {
	var paths []string
	err := winclip.Do(func() error {
		hDrop, _, _ := procGetClipboardData.Call(cfHDROP)
		if hDrop == 0 {
			// No CF_HDROP on the clipboard means no files were copied.
			return nil
		}

		// DragQueryFileW(hDrop, 0xFFFFFFFF, nil, 0) returns the file count.
		count, _, _ := procDragQueryFileW.Call(hDrop, 0xFFFFFFFF, 0, 0)
		paths = make([]string, 0, count)
		for i := range count {
			length, _, _ := procDragQueryFileW.Call(hDrop, i, 0, 0)
			if length == 0 {
				continue
			}
			buf := make([]uint16, length+1) // room for the terminating null
			procDragQueryFileW.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			paths = append(paths, windows.UTF16ToString(buf))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read local file clipboard: %w", err)
	}
	return paths, nil
}

func setLocalClipboardFiles(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	buf := encodeHDROP(paths)

	hMem, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(buf)))
	if hMem == 0 {
		return fmt.Errorf("set local file clipboard: GlobalAlloc: %w", err)
	}
	ptr, _, err := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("set local file clipboard: GlobalLock: %w", err)
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(buf)), buf)
	procGlobalUnlock.Call(hMem)

	owned := false
	err = winclip.Do(func() error {
		procEmptyClipboard.Call() // required before taking ownership of the clipboard
		if r, _, err := procSetClipboardData.Call(cfHDROP, hMem); r == 0 {
			return fmt.Errorf("SetClipboardData: %w", err)
		}
		owned = true // SetClipboardData took ownership of hMem; the clipboard frees it.
		return nil
	})
	if !owned {
		procGlobalFree.Call(hMem) // ownership not transferred
	}
	if err != nil {
		return fmt.Errorf("set local file clipboard: %w", err)
	}
	return nil
}
