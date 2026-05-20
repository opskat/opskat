package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathTraversesSymlink(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	linkDir := filepath.Join(root, "link")
	if err = os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	t.Run("real path returns false", func(t *testing.T) {
		t.Parallel()
		if pathTraversesSymlink(filepath.Join(realDir, "sub", "nested")) {
			t.Fatalf("real path should not report symlink")
		}
	})

	t.Run("path under symlink returns true", func(t *testing.T) {
		t.Parallel()
		if !pathTraversesSymlink(filepath.Join(linkDir, "sub", "nested")) {
			t.Fatalf("path under symlinked dir should report symlink")
		}
	})

	t.Run("symlink itself returns true", func(t *testing.T) {
		t.Parallel()
		if !pathTraversesSymlink(linkDir) {
			t.Fatalf("symlink target itself should report symlink")
		}
	})

	t.Run("nonexistent path under real dir returns false", func(t *testing.T) {
		t.Parallel()
		if pathTraversesSymlink(filepath.Join(realDir, "does", "not", "exist")) {
			t.Fatalf("nonexistent path under real dir should not report symlink")
		}
	})
}
