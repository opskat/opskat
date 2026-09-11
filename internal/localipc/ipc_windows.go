//go:build windows

package localipc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// pipeIdentity resolves the directory through an open handle before deriving a
// per-user endpoint. The final NT path unifies Junctions, drive aliases and
// short names without depending on filesystem-specific file ID widths.
// Never resolve the .sock entry: it may be a legacy AF_UNIX reparse point.
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
	f, err := os.Open(dir)
	if err != nil {
		return "", "", fmt.Errorf("open IPC directory %s: %w", dir, err)
	}
	defer func() { _ = f.Close() }()
	// FILE_NAME_NORMALIZED (0) | VOLUME_NAME_NT (2). Resolve the final target,
	// not the path spelling used to open it; DOS drive mappings may differ.
	buf := make([]uint16, 256)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 2)
		if err != nil {
			return "", "", fmt.Errorf("resolve IPC directory %s: %w", dir, err)
		}
		if n < uint32(len(buf)) {
			dir = windows.UTF16ToString(buf[:n])
			break
		}
		buf = make([]uint16, n+1)
	}
	identity := sid + "\x00" + dir + "\x00" + filepath.Base(path)
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
