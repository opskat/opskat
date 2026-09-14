//go:build !windows

package localipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// DialContext connects to a Unix-domain socket.
func DialContext(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

// Listen preserves Unix socket paths and owner-only permissions. A failed
// connection is not by itself evidence of a stale socket (e.g. EACCES).
func Listen(path string) (net.Listener, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to remove non-socket IPC path %s", path)
		}
		conn, dialErr := Dial(path)
		if dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("another instance is already listening on %s", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return nil, fmt.Errorf("probe existing socket %s: %w", path, dialErr)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect socket %s: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("restrict socket %s: %w", path, err)
	}
	return listener, nil
}
