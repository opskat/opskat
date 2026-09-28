package netdial

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
)

// fakeDNS 是只应答 A 记录的最小 UDP DNS 服务：answers 命中回对应 IPv4，未命中回 NXDOMAIN，
// AAAA 一律回空的 NOERROR。queries 统计收到的查询数。
type fakeDNS struct {
	answers map[string]net.IP
	queries atomic.Int32
}

func startFakeDNS(t *testing.T, answers map[string]net.IP) (*fakeDNS, *net.Resolver) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	s := &fakeDNS{answers: answers}
	go s.serve(pc)
	addr := pc.LocalAddr().String()
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", addr)
	}}
	return s, resolver
}

func (s *fakeDNS) serve(pc net.PacketConn) {
	buf := make([]byte, 512)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		s.queries.Add(1)
		q := buf[:n]
		var labels []string
		i := 12
		for q[i] != 0 {
			labels = append(labels, string(q[i+1:i+1+int(q[i])]))
			i += int(q[i]) + 1
		}
		qtype := binary.BigEndian.Uint16(q[i+1:])
		resp := append([]byte{}, q[:i+5]...)
		resp[2], resp[3] = 0x81, 0x80 // response, RD+RA, NOERROR
		ip, ok := s.answers[strings.ToLower(strings.Join(labels, "."))]
		switch {
		case !ok:
			resp[3] |= 0x03 // NXDOMAIN
		case qtype == 1:
			resp[7] = 1 // ANCOUNT
			resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
			resp = append(resp, ip.To4()...)
		}
		_, _ = pc.WriteTo(resp, addr)
	}
}

// listenLocal 在 127.0.0.1 上监听并持续接受连接，返回端口。
func listenLocal(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	return port
}

func useUnicastResolver(t *testing.T, r *net.Resolver) {
	t.Helper()
	orig := unicastResolver
	unicastResolver = r
	t.Cleanup(func() { unicastResolver = orig })
}

func TestDialContext(t *testing.T) {
	loopback := net.ParseIP("127.0.0.1")

	Convey(".local 主机名经单播 DNS 解析后拨号，不依赖系统解析器", t, func() {
		_, unicast := startFakeDNS(t, map[string]net.IP{"srv01.corp.local": loopback})
		useUnicastResolver(t, unicast)
		port := listenLocal(t)

		d := &Dialer{}
		conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("SRV01.Corp.LOCAL.", port))
		So(err, ShouldBeNil)
		So(conn.RemoteAddr().String(), ShouldEqual, net.JoinHostPort("127.0.0.1", port))
		_ = conn.Close()
	})

	Convey("单播 DNS 查不到的 .local 交回系统解析器（保留 Bonjour 主机的 mDNS 解析）", t, func() {
		_, unicast := startFakeDNS(t, nil)
		useUnicastResolver(t, unicast)
		_, system := startFakeDNS(t, map[string]net.IP{"printer.local": loopback})
		port := listenLocal(t)

		d := &Dialer{Dialer: net.Dialer{Resolver: system}}
		conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("printer.local", port))
		So(err, ShouldBeNil)
		So(conn.RemoteAddr().String(), ShouldEqual, net.JoinHostPort("127.0.0.1", port))
		_ = conn.Close()
	})

	Convey("非 .local 主机名照常走系统解析器，不查单播 DNS", t, func() {
		dns, unicast := startFakeDNS(t, map[string]net.IP{"db.corp.example": loopback})
		useUnicastResolver(t, unicast)
		_, system := startFakeDNS(t, map[string]net.IP{"db.corp.example": loopback})
		port := listenLocal(t)

		d := &Dialer{Dialer: net.Dialer{Resolver: system}}
		conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("db.corp.example", port))
		So(err, ShouldBeNil)
		So(dns.queries.Load(), ShouldEqual, 0)
		_ = conn.Close()
	})
}
