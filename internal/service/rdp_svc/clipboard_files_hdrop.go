package rdp_svc

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// encodeHDROP builds a Win32 CF_HDROP payload (a DROPFILES header followed by a
// wide-char file list) for the given absolute paths. The file list is a sequence
// of UTF-16LE, null-terminated paths closed by an extra null terminator. It is
// pure so the buffer layout can be tested without the clipboard syscalls; the
// Windows clipboard glue lives in clipboard_files_windows.go.
func encodeHDROP(paths []string) []byte {
	var buf bytes.Buffer
	// DROPFILES { DWORD pFiles; POINT pt; BOOL fNC; BOOL fWide; } — 20 bytes.
	header := make([]byte, 20)
	binary.LittleEndian.PutUint32(header[0:4], 20)  // pFiles: offset to file list
	binary.LittleEndian.PutUint32(header[16:20], 1) // fWide: TRUE => UTF-16 list
	buf.Write(header)

	var char [2]byte
	for _, path := range paths {
		for _, unit := range utf16.Encode([]rune(path)) {
			binary.LittleEndian.PutUint16(char[:], unit)
			buf.Write(char[:])
		}
		buf.Write([]byte{0, 0}) // terminate this path
	}
	buf.Write([]byte{0, 0}) // terminate the list
	return buf.Bytes()
}
