//go:build linux

package sshagent

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// peerEUID returns the effective UID of the socket peer when the OS provides
// it, and whether the information was available. Linux exposes it via
// SO_PEERCRED.
func peerEUID(c syscall.Conn) (int, bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	var uid int
	var ok bool
	if err := raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			return
		}
		uid = int(cred.Uid)
		ok = true
	}); err != nil {
		return 0, false
	}
	return uid, ok
}
