package opskat

import (
	"context"
	"net"
	"time"
)

// DialContext opens a TCP connection via the host's IO bridge.
// Its signature matches kafka-go Transport.Dial for drop-in use.
// ctx is consulted only for pre-dial cancellation; per-call deadlines
// should be set on the returned net.Conn via SetDeadline / SetReadDeadline.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h, err := IOOpen("tcp", map[string]any{"addr": addr})
	if err != nil {
		return nil, err
	}
	return &tcpConn{
		h:      h,
		remote: &addrStr{network: network, addr: addr},
		local:  &addrStr{network: network, addr: "wasm"},
	}, nil
}

// Dial is a convenience wrapper without ctx.
func Dial(network, addr string) (net.Conn, error) {
	return DialContext(context.Background(), network, addr)
}

type tcpConn struct {
	h             *IOHandle
	remote, local net.Addr
}

func (c *tcpConn) Read(p []byte) (int, error)  { return c.h.Read(p) }
func (c *tcpConn) Write(p []byte) (int, error) { return c.h.Write(p) }
func (c *tcpConn) Close() error                { return c.h.Close() }
func (c *tcpConn) LocalAddr() net.Addr         { return c.local }
func (c *tcpConn) RemoteAddr() net.Addr        { return c.remote }

func (c *tcpConn) SetDeadline(t time.Time) error {
	return hostIOSetDeadline(c.h.ID(), "both", toUnixNanos(t))
}

func (c *tcpConn) SetReadDeadline(t time.Time) error {
	return hostIOSetDeadline(c.h.ID(), "read", toUnixNanos(t))
}

func (c *tcpConn) SetWriteDeadline(t time.Time) error {
	return hostIOSetDeadline(c.h.ID(), "write", toUnixNanos(t))
}

func toUnixNanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

type addrStr struct {
	network string
	addr    string
}

func (a *addrStr) Network() string { return a.network }
func (a *addrStr) String() string  { return a.addr }
