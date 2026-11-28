//go:build darwin

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
		xucred *unix.Xucred
		scErr  error
	)
	if err := raw.Control(func(fd uintptr) {
		xucred, scErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return err
	}
	if scErr != nil {
		return scErr
	}
	if !peerUIDAllowed(int(xucred.Uid), expectedUID) {
		return fmt.Errorf("peer uid %d does not match expected uid %d", xucred.Uid, expectedUID)
	}
	return nil
}
