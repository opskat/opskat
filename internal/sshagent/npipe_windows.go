//go:build windows

package sshagent

import (
	"context"
	"os"

	"golang.org/x/sys/windows"
)

// dialNamedPipe connects to a local OpenSSH-compatible named pipe. OpenSSH's
// ssh-agent requires SECURITY_SQOS_PRESENT | SECURITY_ANONYMOUS so it verifies
// the caller's identity instead of impersonating it.
func dialNamedPipe(ctx context.Context, name string) (transport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED|
			windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS,
		0,
	)
	if err != nil {
		return nil, err
	}
	// The handle is OVERLAPPED, so os.NewFile wires it to the runtime poller
	// and SetDeadline works (a failed deadline still degrades gracefully: the
	// context watcher closes the transport on cancellation).
	return os.NewFile(uintptr(handle), name), nil
}
