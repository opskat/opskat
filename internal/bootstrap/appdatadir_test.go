package bootstrap

import (
	"runtime"
	"strings"
	"testing"
)

func TestAppDataDir(t *testing.T) {
	t.Run("便携模式返回便携目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return "/tmp/opskat-portable/data" }

		if got := AppDataDir(); got != "/tmp/opskat-portable/data" {
			t.Errorf("AppDataDir() = %q, 期望 %q", got, "/tmp/opskat-portable/data")
		}
	})

	t.Run("非便携模式返回平台默认目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return "" }

		got := AppDataDir()

		if got == "" {
			t.Fatal("AppDataDir() 返回空")
		}
		if !strings.HasSuffix(got, "opskat") {
			t.Errorf("AppDataDir() = %q, 期望以 opskat 结尾", got)
		}
		switch runtime.GOOS {
		case "darwin":
			if !strings.Contains(got, "Application Support") {
				t.Errorf("AppDataDir() = %q, darwin 期望包含 Application Support", got)
			}
		case "linux":
			if !strings.Contains(got, ".config") {
				t.Errorf("AppDataDir() = %q, linux 期望包含 .config", got)
			}
		}
	})
}
