package extension

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// recordingHost captures OpenIO calls to verify delegation through capHost.
// Non-OpenIO methods embed HostProvider (nil) and panic if called; the tests
// below only exercise OpenIO.
type recordingHost struct {
	HostProvider
	lastParams IOOpenParams
	opened     int
}

func (r *recordingHost) OpenIO(params IOOpenParams) (*IOResource, error) {
	r.opened++
	r.lastParams = params
	return &IOResource{}, nil
}

// TestCapHostTCPPassthrough pins the current (Phase 1) behavior that capHost
// does NOT gate OpenIO(type="tcp") — the wazero host module exposes TCP to
// every loaded extension because only first-party extensions (Kafka) build
// against it. When a Capabilities.TCP + CheckTCPAddr gate lands post-Phase 1,
// this test WILL (and should) fail — update it alongside the gate so third-
// party extensions can't open raw sockets without declaring the capability.
func TestCapHostTCPPassthrough(t *testing.T) {
	Convey("Given a capHost wrapping a manifest with no capabilities", t, func() {
		inner := &recordingHost{}
		manifest := &Manifest{Name: "no-caps", Version: "1.0.0"}
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")

		Convey("OpenIO(type=tcp) is not blocked and is delegated to the inner host", func() {
			res, err := ch.OpenIO(IOOpenParams{Type: "tcp", Addr: "example.com:9092"})
			So(err, ShouldBeNil)
			So(res, ShouldNotBeNil)
			So(inner.opened, ShouldEqual, 1)
			So(inner.lastParams.Type, ShouldEqual, "tcp")
			So(inner.lastParams.Addr, ShouldEqual, "example.com:9092")
		})

		Convey("OpenIO(type=http) is still gated by the allowlist", func() {
			_, err := ch.OpenIO(IOOpenParams{Type: "http", URL: "https://example.com/foo"})
			So(err, ShouldNotBeNil) // allowlist is empty, so reject
		})
	})
}

// Compile-time assertion that recordingHost satisfies HostProvider.
var _ HostProvider = (*recordingHost)(nil)
