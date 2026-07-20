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

func TestSameDir(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatalf("准备 data 目录失败: %v", err)
	}

	t.Run("相对路径与绝对路径指向同一目录", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("读取工作目录失败: %v", err)
		}
		relative, err := filepath.Rel(cwd, dataDir)
		if err != nil {
			t.Fatalf("构造相对路径失败: %v", err)
		}

		if !SameDir(relative, dataDir) {
			t.Fatal("相对路径与绝对路径应识别为同一目录")
		}
	})

	t.Run("包含父目录跳转的路径指向同一目录", func(t *testing.T) {
		pathWithParent := filepath.Join(dataDir, "child", "..")
		if !SameDir(pathWithParent, dataDir) {
			t.Fatal("包含 .. 的等价路径应识别为同一目录")
		}
	})

	t.Run("符号链接指向同一目录", func(t *testing.T) {
		link := filepath.Join(base, "data-link")
		if err := os.Symlink(dataDir, link); err != nil {
			t.Skipf("当前平台无法创建符号链接: %v", err)
		}
		if !SameDir(link, dataDir) {
			t.Fatal("符号链接与目标应识别为同一目录")
		}
	})

	t.Run("不同目录不相等", func(t *testing.T) {
		other := filepath.Join(base, "other")
		if err := os.Mkdir(other, 0o755); err != nil {
			t.Fatalf("准备其他目录失败: %v", err)
		}
		if SameDir(other, dataDir) {
			t.Fatal("不同目录不应识别为同一目录")
		}
	})
}
