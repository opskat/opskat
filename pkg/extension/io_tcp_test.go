package extension

import (
	"net"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestIOOpenTCP(t *testing.T) {
	Convey("Given a DefaultHostProvider", t, func() {
		// Start a trivial TCP echo server
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		So(err, ShouldBeNil)
		defer ln.Close() //nolint:errcheck

		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() //nolint:errcheck
			buf := make([]byte, 1024)
			n, _ := c.Read(buf)
			_, _ = c.Write(buf[:n]) // echo server; write failure is irrelevant in test
		}()

		h := NewDefaultHostProvider(DefaultHostConfig{})

		Convey("IOOpen(tcp) should succeed with valid addr", func() {
			id, _, err := h.IOOpen(IOOpenParams{Type: "tcp", Addr: ln.Addr().String()})
			So(err, ShouldBeNil)
			So(id, ShouldBeGreaterThan, uint32(0))

			Convey("Write and Read should round-trip", func() {
				n, err := h.IOWrite(id, []byte("ping"))
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 4)

				data, err := h.IORead(id, 16)
				So(err, ShouldBeNil)
				So(string(data), ShouldEqual, "ping")

				So(h.IOClose(id), ShouldBeNil)
			})
		})

		Convey("IOOpen(tcp) with invalid addr should fail", func() {
			_, _, err := h.IOOpen(IOOpenParams{Type: "tcp", Addr: "localhost:1"})
			So(err, ShouldNotBeNil)
		})
	})
}
