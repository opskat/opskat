package sftpiotest

import (
	"path/filepath"
	"runtime"
)

// RemotePath is the SFTP path at which the test servers here (and any pkg/sftp server
// over the local filesystem) expose the local file local. SFTP paths are POSIX; on
// Windows pkg/sftp presents a drive path as "/C:/Users/..." and maps it back to
// "C:\Users\...", so a test must not hand it the native path.
func RemotePath(local string) string {
	if runtime.GOOS == "windows" {
		return "/" + filepath.ToSlash(local)
	}
	return local
}
