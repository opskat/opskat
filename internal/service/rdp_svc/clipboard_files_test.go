package rdp_svc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClipboardFilesFromPathsStreamsRequestedRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := clipboardFilesFromPaths([]string{path})
	if err != nil {
		t.Fatalf("clipboardFilesFromPaths: %v", err)
	}
	if len(files) != 1 || files[0].Name != "report.txt" || files[0].Size != 11 {
		t.Fatalf("files = %+v", files)
	}
	data, err := files[0].ReadRange(6, 5)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if string(data) != "world" {
		t.Fatalf("data = %q", data)
	}
}

func TestClipboardFilesFromPathsRejectsDirectories(t *testing.T) {
	_, err := clipboardFilesFromPaths([]string{t.TempDir()})
	if err == nil {
		t.Fatal("expected directory to be rejected")
	}
}
