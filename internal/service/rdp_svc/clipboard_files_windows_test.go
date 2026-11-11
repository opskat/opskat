//go:build windows

package rdp_svc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/opskat/opskat/internal/pkg/winclip"
)

func TestLocalClipboardFiles_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "目录 b", "c.log")}
	for _, p := range want {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := setLocalClipboardFiles(want); err != nil {
		t.Fatalf("setLocalClipboardFiles: %v", err)
	}
	got, err := readLocalClipboardFilePaths()
	if err != nil {
		t.Fatalf("readLocalClipboardFilePaths: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestReadLocalClipboardFilePaths_NoFilesIsEmpty(t *testing.T) {
	if err := winclip.Do(func() error {
		_, _, _ = procEmptyClipboard.Call()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got, err := readLocalClipboardFilePaths()
	if err != nil {
		t.Fatalf("readLocalClipboardFilePaths: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no files, got %q", got)
	}
}
