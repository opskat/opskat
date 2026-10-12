package extension

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type zipEntry struct {
	name string
	data []byte
}

func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	f, err := os.Create(path) //nolint:gosec // test TempDir
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		fw, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func setZipLimits(t *testing.T, size int64, entries int) {
	t.Helper()
	oldSize, oldEntries := maxZipExtractedBytes, maxZipEntries
	maxZipExtractedBytes, maxZipEntries = size, entries
	t.Cleanup(func() { maxZipExtractedBytes, maxZipEntries = oldSize, oldEntries })
}

func TestExtractZipLimits(t *testing.T) {
	t.Run("total decompressed size over the limit fails", func(t *testing.T) {
		setZipLimits(t, 1024, 100)
		zipPath := filepath.Join(t.TempDir(), "big.zip")
		// Highly compressible: the archive is tiny, the decompressed bytes are not.
		writeZip(t, zipPath, []zipEntry{
			{"a.bin", []byte(strings.Repeat("x", 600))},
			{"b.bin", []byte(strings.Repeat("y", 600))},
		})
		if err := extractZip(zipPath, t.TempDir()); err == nil {
			t.Fatal("expected size limit error")
		}
	})

	t.Run("total size at the limit passes", func(t *testing.T) {
		setZipLimits(t, 1200, 100)
		zipPath := filepath.Join(t.TempDir(), "ok.zip")
		writeZip(t, zipPath, []zipEntry{
			{"a.bin", []byte(strings.Repeat("x", 600))},
			{"b.bin", []byte(strings.Repeat("y", 600))},
		})
		if err := extractZip(zipPath, t.TempDir()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("entry count over the limit fails", func(t *testing.T) {
		setZipLimits(t, 1<<20, 3)
		zipPath := filepath.Join(t.TempDir(), "many.zip")
		writeZip(t, zipPath, []zipEntry{{"1", nil}, {"2", nil}, {"3", nil}, {"4", nil}})
		if err := extractZip(zipPath, t.TempDir()); err == nil {
			t.Fatal("expected entry count error")
		}
	})

	t.Run("path traversal is still rejected", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "evil.zip")
		writeZip(t, zipPath, []zipEntry{{"../escape.txt", []byte("x")}})
		dest := filepath.Join(t.TempDir(), "dest")
		if err := os.MkdirAll(dest, 0755); err != nil {
			t.Fatal(err)
		}
		if err := extractZip(zipPath, dest); err == nil {
			t.Fatal("expected unsafe path error")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
			t.Fatal("file escaped destination")
		}
	})
}

// An over-limit zip must fail the install without touching the installed version
// or leaving anything behind in the extensions directory.
func TestManagerInstallZipOverLimitKeepsInstalled(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logger := zap.NewNop()
	useDescribeCache(t, newFakeDescribeCache(stubWasm, cannedDescriptor))
	mgr := NewManager(dir, func(string) HostProvider {
		return NewDefaultHostProvider(DefaultHostConfig{Logger: logger})
	}, logger)
	t.Cleanup(func() { mgr.Close(ctx) })

	src := t.TempDir()
	v1 := filepath.Join(src, "v1")
	writeMinimalExtension(t, v1, "hot")
	if _, err := mgr.Install(ctx, v1); err != nil {
		t.Fatal(err)
	}
	old := mgr.GetExtension("hot")

	manifest, err := os.ReadFile(filepath.Join(v1, "manifest.json")) //nolint:gosec // test TempDir
	if err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(src, "hot.zip")
	writeZip(t, zipPath, []zipEntry{
		{"manifest.json", manifest},
		{"main.wasm", stubWasm},
		{"pad.bin", []byte(strings.Repeat("p", 4096))},
	})
	setZipLimits(t, 1024, 100)

	if _, err := mgr.Install(ctx, zipPath); err == nil {
		t.Fatal("expected install to fail")
	}
	if mgr.GetExtension("hot") != old || old.Plugin.closed.Load() {
		t.Fatal("installed extension changed")
	}
	if got := extensionsDirEntries(t, dir); len(got) != 1 || got[0] != "hot" {
		t.Fatalf("leftover in extensions dir: %v", got)
	}
}
