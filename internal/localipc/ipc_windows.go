//go:build windows

package localipc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// pipeIdentity uses the directory's filesystem identity, so Junctions, relative
// paths, drive-letter casing and short-name aliases resolve to the same pipe.
// The directory already exists after bootstrap. Never fall back to a global
// pipe name on a lookup failure: that could connect to a different data set.
func pipeIdentity(path string) (name, sid string, err error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", "", fmt.Errorf("get IPC user: %w", err)
	}
	sid = user.User.Sid.String()
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", "", err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", "", fmt.Errorf("open IPC directory %s: %w", dir, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", "", fmt.Errorf("identify IPC directory %s: %w", dir, err)
	}
	identity := fmt.Sprintf("%s\x00%08x:%08x:%08x\x00%s", sid,
		info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, filepath.Base(path))
	return fmt.Sprintf(`\\.\pipe\opskat-%x`, sha256.Sum256([]byte(identity))), sid, nil
}

// Listen creates an exclusive, byte-stream pipe restricted to the current user.
// go-winio rejects remote clients and atomically reserves the first instance.
// Windows releases pipe handles on process exit; no .sock file is touched.
func Listen(path string) (net.Listener, error) {
	name, sid, err := pipeIdentity(path)
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
}

// DialContext connects without permitting the server to impersonate the client
// (go-winio uses SECURITY_ANONYMOUS). Application token checks remain separate.
func DialContext(ctx context.Context, path string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, _, err := pipeIdentity(path)
	if err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, name)
}
