package embedded

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
