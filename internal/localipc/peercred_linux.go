//go:build linux

package localipc

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func verifyUnixPeerUID(conn net.Conn, expectedUID int) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("unexpected connection type %T", conn)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var (
		ucred *unix.Ucred
		scErr error
	)
	if err := raw.Control(func(fd uintptr) {
		ucred, scErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if scErr != nil {
		return scErr
	}
	if !peerUIDAllowed(int(ucred.Uid), expectedUID) {
		return fmt.Errorf("peer uid %d does not match expected uid %d", ucred.Uid, expectedUID)
	}
	return nil
}
