package extension

import (
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBridge(t *testing.T) {
	Convey("Bridge", t, func() {
		bridge := NewBridge()

		ext := &Extension{
			Name: "oss",
			Manifest: &Manifest{
				Manifest: extmanifest.Manifest{Name: "oss", Version: "1.0.0"},
			},
		}
		bridge.Register(ext)

		Convey("Get returns a loaded extension by name", func() {
			So(bridge.Get("oss"), ShouldEqual, ext)
			So(bridge.Get("nope"), ShouldBeNil)
		})

		Convey("ListNames reports loaded extensions", func() {
			So(bridge.ListNames(), ShouldResemble, []string{"oss"})
		})

		Convey("Unregister drops the extension", func() {
			bridge.Unregister("oss")
			So(bridge.ListNames(), ShouldBeEmpty)
			So(bridge.Get("oss"), ShouldBeNil)
		})
	})
}
