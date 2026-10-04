//go:build !wasip1

package opskat

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTCPDialBasic(t *testing.T) {
	Convey("Given a TestHost with a mock TCP echo server", t, func() {
		th := NewTestHost(WithMockTCP(func(addr string) (net.Conn, error) {
			a, b := net.Pipe()
			go func() {
				buf := make([]byte, 256)
				n, _ := a.Read(buf)
				_, _ = a.Write(buf[:n]) // echo pipe
			}()
			return b, nil
		}))
		defer th.Close()

		Convey("DialContext returns a working net.Conn", func() {
			conn, err := DialContext(context.Background(), "tcp", "broker:9092")
			So(err, ShouldBeNil)
			defer func() { _ = conn.Close() }()

			n, err := conn.Write([]byte("hi"))
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)

			buf := make([]byte, 8)
			n, err = conn.Read(buf)
			So(err, ShouldBeNil)
			So(string(buf[:n]), ShouldEqual, "hi")
		})
	})
}

func TestTCPSetDeadline(t *testing.T) {
	Convey("SetDeadline on tcpConn", t, func() {
		th := NewTestHost(WithMockTCP(func(addr string) (net.Conn, error) {
			_, b := net.Pipe()
			return b, nil
		}))
		defer th.Close()

		conn, err := DialContext(context.Background(), "tcp", "x:9092")
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		Convey("SetReadDeadline propagates to underlying conn", func() {
			err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			So(err, ShouldBeNil)

			// Read should block briefly then timeout.
			buf := make([]byte, 8)
			_, err = conn.Read(buf)
			So(err, ShouldNotBeNil)
			var netErr net.Error
			So(errors.As(err, &netErr), ShouldBeTrue)
		})
	})
}
