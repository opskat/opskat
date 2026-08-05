//go:build !windows

package sshagent

import (
	"context"
	"errors"
)

// dialNamedPipe is unreachable on non-Windows platforms: Open rejects pipe
// endpoint kinds before dialing. It exists so the transport always has a dial
// path on every platform.
func dialNamedPipe(ctx context.Context, name string) (transport, error) {
	return nil, errors.New("sshagent: named pipes are not supported on this platform")
}
