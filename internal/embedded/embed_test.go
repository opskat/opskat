package embedded

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpsctlNeedsUpdate(t *testing.T) {
	original := opsctlBinary
	t.Cleanup(func() { opsctlBinary = original })
	opsctlBinary = []byte("current opsctl")

	installed := filepath.Join(t.TempDir(), "opsctl")
	if err := os.WriteFile(installed, []byte("older opsctl"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	needsUpdate, err := OpsctlNeedsUpdate(installed)
	if err != nil {
		t.Fatalf("OpsctlNeedsUpdate older binary: %v", err)
	}
	if !needsUpdate {
		t.Fatal("older installed binary should need an update")
	}

	if err := os.WriteFile(installed, opsctlBinary, 0o755); err != nil {
		t.Fatalf("WriteFile current binary: %v", err)
	}
	needsUpdate, err = OpsctlNeedsUpdate(installed)
	if err != nil {
		t.Fatalf("OpsctlNeedsUpdate current binary: %v", err)
	}
	if needsUpdate {
		t.Fatal("current installed binary should not need an update")
	}
}

func TestDefaultInstallDir(t *testing.T) {
	t.Run("便携模式返回可执行文件所在目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return filepath.Join("/tmp/opskat-portable", "data") }

		got := DefaultInstallDir()

		if got != "/tmp/opskat-portable" {
			t.Errorf("DefaultInstallDir() = %q, 期望 %q（data 目录的父目录，即 exe 同级）", got, "/tmp/opskat-portable")
		}
	})

	t.Run("非便携模式返回平台默认安装目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return "" }

		got := DefaultInstallDir()

		if got == "" {
			t.Fatal("DefaultInstallDir() 返回空")
		}
		if runtime.GOOS != "windows" && !strings.HasSuffix(got, filepath.Join(".local", "bin")) {
			t.Errorf("DefaultInstallDir() = %q, 非 windows 期望以 .local/bin 结尾", got)
		}
	})
}
