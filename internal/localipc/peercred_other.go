//go:build !windows && !linux && !darwin

package localipc

import "net"

func verifyUnixPeerUID(_ net.Conn, _ int) error {
	return nil
}
