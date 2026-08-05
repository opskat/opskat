//go:build !linux && !darwin

package sshagent

import "syscall"

// peerEUID reports that peer credentials are not available; Open then skips
// the UID check (the OS does not provide the information).
func peerEUID(c syscall.Conn) (int, bool) {
	return 0, false
}
