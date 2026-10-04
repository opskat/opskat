package extension

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBridge(t *testing.T) {
	Convey("Bridge", t, func() {
		bridge := NewBridge()

		ext := &Extension{
			Name: "oss",
			Manifest: &Manifest{
				Name:       "oss",
				Version:    "1.0.0",
				AssetTypes: []AssetTypeDef{{Type: "oss", I18n: I18nName{Name: "assetType.oss.name"}}},
				Policies:   PoliciesDef{Type: "ext:oss"},
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

		Convey("GetExtensionByAssetType resolves the owning extension", func() {
			So(bridge.GetExtensionByAssetType("oss"), ShouldEqual, ext)
			So(bridge.GetExtensionByAssetType("ssh"), ShouldBeNil)
		})

		Convey("Unregister drops the extension", func() {
			bridge.Unregister("oss")
			So(bridge.ListNames(), ShouldBeEmpty)
			So(bridge.GetExtensionByAssetType("oss"), ShouldBeNil)
		})
	})
}
