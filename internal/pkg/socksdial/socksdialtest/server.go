// Package socksdialtest 提供测试用的极简 SOCKS5 服务器与 echo 服务,供 socksdial 与 connpool 测试共用。
package socksdialtest

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
)

// StartEcho 启动一个回显 TCP 服务,返回监听地址。
func StartEcho(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// Start 启动一个极简 SOCKS5 服务(仅 CONNECT),返回监听地址。
// user 为空表示 no-auth,否则要求 RFC 1929 用户名/密码认证。
func Start(t testing.TB, user, pass string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSocks5(conn, user, pass)
		}
	}()
	return ln.Addr().String()
}

func handleSocks5(conn net.Conn, user, pass string) {
	defer func() { _ = conn.Close() }()
	// 协商: VER NMETHODS METHODS...
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if user != "" {
		// 要求用户名/密码认证
		if _, err := conn.Write([]byte{5, 2}); err != nil {
			return
		}
		// RFC 1929: VER ULEN UNAME PLEN PASSWD
		authHead := make([]byte, 2)
		if _, err := io.ReadFull(conn, authHead); err != nil || authHead[0] != 1 {
			return
		}
		uname := make([]byte, authHead[1])
		if _, err := io.ReadFull(conn, uname); err != nil {
			return
		}
		plen := make([]byte, 1)
		if _, err := io.ReadFull(conn, plen); err != nil {
			return
		}
		passwd := make([]byte, plen[0])
		if _, err := io.ReadFull(conn, passwd); err != nil {
			return
		}
		if string(uname) != user || string(passwd) != pass {
			_, _ = conn.Write([]byte{1, 1}) // 认证失败
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	} else {
		if _, err := conn.Write([]byte{5, 0}); err != nil {
			return
		}
	}
	// 请求: VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3: // 域名
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", binary.BigEndian.Uint16(portBuf))))
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = target.Close() }()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(target, conn) }()
	_, _ = io.Copy(conn, target)
}
