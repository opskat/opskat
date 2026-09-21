package desktopopen

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Open 用系统默认关联程序打开文件或目录（macOS open / Windows start / Linux xdg-open）。
func Open(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{path}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", "", path}
	default:
		cmd = "xdg-open"
		args = []string{path}
	}
	c := exec.Command(cmd, args...) //nolint:gosec // user-initiated open of app-managed temp log file
	return c.Start()
}
