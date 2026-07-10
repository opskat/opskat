package rdp_svc

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// decodeHDROPFileList parses the wide-char file list of a DROPFILES buffer back
// into paths, mirroring how the Win32 shell reads CF_HDROP. Test-only helper so
// the pure encoder can be verified on any host without the clipboard syscalls.
func decodeHDROPFileList(t *testing.T, buf []byte) []string {
	t.Helper()
	if len(buf) < 20 {
		t.Fatalf("buffer shorter than DROPFILES header: %d bytes", len(buf))
	}
	offset := binary.LittleEndian.Uint32(buf[0:4])
	list := buf[offset:]
	if len(list)%2 != 0 {
		t.Fatalf("file list not 16-bit aligned: %d bytes", len(list))
	}
	u16 := make([]uint16, len(list)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(list[i*2:])
	}
	if len(u16) < 2 || u16[len(u16)-1] != 0 || u16[len(u16)-2] != 0 {
		t.Fatalf("file list not double-null terminated: %v", u16)
	}
	var paths []string
	start := 0
	for i, c := range u16 {
		if c != 0 {
			continue
		}
		if i > start {
			paths = append(paths, string(utf16.Decode(u16[start:i])))
		}
		start = i + 1
	}
	return paths
}

func TestEncodeHDROPProducesWideNullTerminatedFileList(t *testing.T) {
	paths := []string{`C:\Users\a\report.txt`, `C:\共享\日志.log`}

	buf := encodeHDROP(paths)

	if got := binary.LittleEndian.Uint32(buf[0:4]); got != 20 {
		t.Fatalf("pFiles offset = %d, want 20 (header size)", got)
	}
	if got := binary.LittleEndian.Uint32(buf[16:20]); got != 1 {
		t.Fatalf("fWide = %d, want 1 (UTF-16 file list)", got)
	}

	got := decodeHDROPFileList(t, buf)
	if len(got) != len(paths) {
		t.Fatalf("decoded %d paths, want %d: %q", len(got), len(paths), got)
	}
	for i := range paths {
		if got[i] != paths[i] {
			t.Fatalf("path[%d] = %q, want %q", i, got[i], paths[i])
		}
	}
}
