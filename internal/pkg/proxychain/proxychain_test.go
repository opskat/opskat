package proxychain

import (
	"errors"
	"net"
	"testing"
	"time"
)

type deadlineErrorConn struct {
	net.Conn
}

func (c deadlineErrorConn) SetDeadline(time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}
func (c deadlineErrorConn) SetReadDeadline(time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}
func (c deadlineErrorConn) SetWriteDeadline(time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}

func TestDeadlineIgnoredConn(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	conn := deadlineIgnoredConn{Conn: deadlineErrorConn{Conn: left}}
	if err := conn.SetDeadline(time.Now()); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := conn.SetWriteDeadline(time.Now()); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
}
