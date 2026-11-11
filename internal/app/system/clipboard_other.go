//go:build !windows

package system

import (
	"context"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// macOS（pbpaste）和 Linux（GTK）上 Wails 对空剪贴板返回 ""，没有 Windows 那个问题。
func readClipboardText(ctx context.Context) (string, error) {
	return wailsRuntime.ClipboardGetText(ctx)
}
