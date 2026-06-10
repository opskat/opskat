package ssh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/opskat/opskat/internal/service/zmodem_svc"
)

func TestZmodemOpenUploadFilesSkipsInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	subdir := filepath.Join(dir, "subdir")
	missing := filepath.Join(dir, "missing.txt")

	if err := os.WriteFile(first, []byte("one"), 0600); err != nil {
		t.Fatalf("WriteFile first: %v", err)
	}
	if err := os.WriteFile(second, []byte("two-two"), 0600); err != nil {
		t.Fatalf("WriteFile second: %v", err)
	}
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	app := &SSH{zmodem: zmodem_svc.New(func(transfer.Progress) {})}
	files, err := app.ZmodemOpenUploadFiles("s1", []string{first, subdir, missing, second})
	if err != nil {
		t.Fatalf("ZmodemOpenUploadFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}

	if files[0].Name != "first.txt" || files[0].Size != 3 {
		t.Fatalf("first file = %+v, want first.txt size 3", files[0])
	}
	if files[1].Name != "second.txt" || files[1].Size != 7 {
		t.Fatalf("second file = %+v, want second.txt size 7", files[1])
	}

	for _, f := range files {
		if f.TransferID == "" {
			t.Fatalf("empty transfer id for %+v", f)
		}
		if _, _, err := app.zmodem.ReadChunk(f.TransferID, 1); err != nil {
			t.Fatalf("ReadChunk(%s): %v", f.TransferID, err)
		}
		if err := app.zmodem.AbortUpload(f.TransferID); err != nil {
			t.Fatalf("AbortUpload(%s): %v", f.TransferID, err)
		}
	}
}
