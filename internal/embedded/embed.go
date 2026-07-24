package embedded

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/opskat/opskat/internal/pkg/portable"
)

// opsctlBinary 由 embed_prod.go (embed_opsctl tag) 或 embed_dev.go 设置
var opsctlBinary []byte

// HasEmbeddedOpsctl 检查是否嵌入了 opsctl 二进制
func HasEmbeddedOpsctl() bool {
	return len(opsctlBinary) > 0
}

// OpsctlNeedsUpdate reports whether path differs from the opsctl binary embedded
// in the running application. Callers should only use it for an installed path.
func OpsctlNeedsUpdate(path string) (bool, error) {
	if len(opsctlBinary) == 0 {
		return false, nil
	}
	installed, err := os.ReadFile(path) //nolint:gosec // path is the detected opsctl executable
	if err != nil {
		return false, fmt.Errorf("read installed opsctl: %w", err)
	}
	return !bytes.Equal(installed, opsctlBinary), nil
}

// portableDir 解析便携数据目录，便携模式外返回 ""。变量而非直接调用，是为了可测。
var portableDir = portable.Dir

// DefaultInstallDir 返回默认安装目录。
// 便携模式下与 opskat.exe 同级（即便携 data 目录的父目录），使 opsctl 与
// 应用认到同一个数据目录；否则 Windows 上与数据目录统一为 %LOCALAPPDATA%\opskat。
func DefaultInstallDir() string {
	if dir := portableDir(); dir != "" {
		return filepath.Dir(dir)
	}
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			home, _ := os.UserHomeDir()
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "opskat")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// InstallOpsctl 将嵌入的 opsctl 二进制写入指定目录
func InstallOpsctl(targetDir string) (string, error) {
	if len(opsctlBinary) == 0 {
		return "", fmt.Errorf("no embedded opsctl binary")
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("create directory failed: %w", err)
	}

	binName := "opsctl"
	if runtime.GOOS == "windows" {
		binName = "opsctl.exe"
	}
	targetPath := filepath.Join(targetDir, binName)

	if err := os.WriteFile(targetPath, opsctlBinary, 0755); err != nil {
		if runtime.GOOS == "windows" && os.IsPermission(err) {
			return "", fmt.Errorf("write binary failed (file may be in use, please close opsctl and retry): %w", err)
		}
		return "", fmt.Errorf("write binary failed: %w", err)
	}

	// 便携模式不改宿主机 PATH：写 HKCU Environment 是实打实的污染，
	// 且便携目录的盘符会变，写进 PATH 也没有意义。
	if portableDir() != "" {
		return targetPath, nil
	}

	// Windows: 将安装目录添加到用户 PATH
	if err := addToUserPath(targetDir); err != nil {
		return targetPath, fmt.Errorf("installed successfully but failed to add to PATH: %w", err)
	}

	return targetPath, nil
}
