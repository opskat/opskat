package portable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirFor(t *testing.T) {
	t.Run("同级存在 data 目录时返回该目录", func(t *testing.T) {
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		if err := os.Mkdir(dataDir, 0o755); err != nil {
			t.Fatalf("准备 data 目录失败: %v", err)
		}

		got := dirFor(filepath.Join(base, "opskat.exe"))

		if got != dataDir {
			t.Errorf("dirFor() = %q, 期望 %q", got, dataDir)
		}
	})

	t.Run("同级无 data 目录时返回空", func(t *testing.T) {
		base := t.TempDir()

		got := dirFor(filepath.Join(base, "opskat.exe"))

		if got != "" {
			t.Errorf("dirFor() = %q, 期望空字符串", got)
		}
	})

	t.Run("同级 data 是文件而非目录时返回空", func(t *testing.T) {
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, "data"), []byte("x"), 0o600); err != nil {
			t.Fatalf("准备 data 文件失败: %v", err)
		}

		got := dirFor(filepath.Join(base, "opskat.exe"))

		if got != "" {
			t.Errorf("dirFor() = %q, 期望空字符串（data 是文件不应触发便携模式）", got)
		}
	})
}
