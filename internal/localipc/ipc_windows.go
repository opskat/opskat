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

// pipeIdentity resolves directory aliases before deriving a per-user endpoint.
// On Windows EvalSymlinks also normalizes drive/component case and short names.
// Resolve the parent, not the .sock entry: that entry need not exist and may be
// a legacy AF_UNIX reparse point. Never fall back to a global pipe on failure.
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
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve IPC directory %s: %w", filepath.Dir(path), err)
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
