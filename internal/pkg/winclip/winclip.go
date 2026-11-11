//go:build windows

// Package winclip is the one place the desktop app touches the Win32 clipboard directly.
//
// Wails' runtime.ClipboardGetText is wrong on Windows in two ways (wails v2.16.0
// internal/frontend/desktop/windows/win32/clipboard.go): a clipboard with no text on it
// (empty, a screenshot, an image) comes back as an error whose errno is 0 — "The
// operation completed successfully" (issue #367) — and text longer than 1<<20 UTF-16
// units is cut off. ReadText replaces it for reads; writes still go through Wails.
package winclip

import (
	"errors"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cfUnicodeText = 13 // CF_UNICODETEXT

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard              = modUser32.NewProc("OpenClipboard")
	procCloseClipboard             = modUser32.NewProc("CloseClipboard")
	procIsClipboardFormatAvailable = modUser32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData           = modUser32.NewProc("GetClipboardData")

	procGlobalLock   = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock = modKernel32.NewProc("GlobalUnlock")
	procGlobalSize   = modKernel32.NewProc("GlobalSize")
)

const (
	openTimeout    = time.Second
	openRetryDelay = 10 * time.Millisecond
)

// Do opens the clipboard, runs fn, then closes it, all on one OS thread: CloseClipboard
// on a different thread than OpenClipboard fails (ERROR_CLIPBOARD_NOT_OPEN) and leaves
// the clipboard held. While another window has the clipboard open, OpenClipboard fails
// with ERROR_ACCESS_DENIED; that is usually momentary, so it is retried for openTimeout.
func Do(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := open(); err != nil {
		return err
	}
	err := fn()
	if r, _, closeErr := procCloseClipboard.Call(); r == 0 {
		err = errors.Join(err, fmt.Errorf("CloseClipboard: %w", closeErr))
	}
	return err
}

func open() error {
	deadline := time.Now().Add(openTimeout)
	for {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("OpenClipboard: %w", err)
		}
		time.Sleep(openRetryDelay)
	}
}

// ReadText returns the clipboard's text, or "" when it holds no text.
func ReadText() (string, error) {
	var text string
	err := Do(func() error {
		// Checked with the clipboard open so the answer can't change before the read.
		// A zero return only means "not there": it doesn't set the last error.
		if r, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
			return nil
		}
		h, _, err := procGetClipboardData.Call(cfUnicodeText)
		if h == 0 {
			return fmt.Errorf("GetClipboardData(CF_UNICODETEXT): %w", err)
		}
		size, _, err := procGlobalSize.Call(h)
		if size == 0 {
			return fmt.Errorf("GlobalSize: %w", err)
		}
		ptr, _, err := procGlobalLock.Call(h)
		if ptr == 0 {
			return fmt.Errorf("GlobalLock: %w", err)
		}
		// GlobalSize can round up; UTF16ToString stops at the terminating NUL.
		text = windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), size/2))
		// Zero with NO_ERROR means "now unlocked" — the expected outcome, not a failure.
		if r, _, err := procGlobalUnlock.Call(h); r == 0 && !errors.Is(err, windows.Errno(windows.NO_ERROR)) {
			return fmt.Errorf("GlobalUnlock: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("read clipboard text: %w", err)
	}
	return text, nil
}
