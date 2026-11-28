//go:build !windows

package localipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
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
	return &peerVerifiedListener{Listener: listener, expectedUID: os.Geteuid()}, nil
}

// peerUIDAllowed reports whether a peer running as uid may talk to a listener owned by
// owner: the same user, or root — which the socket's 0600 mode never kept out, so
// `sudo opsctl …` reaching the user's desktop keeps working.
func peerUIDAllowed(uid, owner int) bool {
	return uid == owner || uid == 0
}

// peerVerifiedListener checks OS-level peer credentials on every accepted Unix
// connection, closing the Listen -> Chmod umask window and rejecting any client
// running under another (non-root) UID.
type peerVerifiedListener struct {
	net.Listener
	expectedUID int
}

func (l *peerVerifiedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := verifyUnixPeerUID(conn, l.expectedUID); err != nil {
			logger.Default().Warn("local IPC connection rejected: peer is not the current user",
				zap.String("addr", l.Addr().String()), zap.Error(err))
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
