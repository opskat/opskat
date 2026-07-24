package policy

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDefaultPolicyRegistry(t *testing.T) {
	Convey("DefaultPolicy Registry", t, func() {
		Convey("未注册类型返回 false", func() {
			_, ok := GetDefaultPolicyOf("nonexistent")
			So(ok, ShouldBeFalse)
		})

		Convey("动态注册和注销", func() {
			RegisterDefaultPolicy("oss", func() any {
				return &CommandPolicy{Groups: []string{"ext:oss:readonly"}}
			})
			defer UnregisterDefaultPolicy("oss")

			p, ok := GetDefaultPolicyOf("oss")
			So(ok, ShouldBeTrue)
			cp, ok := p.(*CommandPolicy)
			So(ok, ShouldBeTrue)
			So(cp.Groups, ShouldResemble, []string{"ext:oss:readonly"})

			UnregisterDefaultPolicy("oss")
			_, ok = GetDefaultPolicyOf("oss")
			So(ok, ShouldBeFalse)
		})

		Convey("覆盖注册", func() {
			RegisterDefaultPolicy("test-type", func() any {
				return &CommandPolicy{Groups: []string{"a"}}
			})
			defer UnregisterDefaultPolicy("test-type")

			RegisterDefaultPolicy("test-type", func() any {
				return &CommandPolicy{Groups: []string{"b"}}
			})

			p, _ := GetDefaultPolicyOf("test-type")
			cp := p.(*CommandPolicy)
			So(cp.Groups, ShouldResemble, []string{"b"})
		})
	})
}

func TestAssetKindRegistry(t *testing.T) {
	Convey("AssetKind Registry", t, func() {
		Convey("注册后可查、注销后消失", func() {
			RegisterAssetKind("faketype", PolicyKindCommand)
			defer UnregisterAssetKind("faketype")

			got, ok := AssetKindOf("faketype")
			So(ok, ShouldBeTrue)
			So(got, ShouldEqual, PolicyKindCommand)

			UnregisterAssetKind("faketype")
			_, ok = AssetKindOf("faketype")
			So(ok, ShouldBeFalse)
		})

		Convey("未注册类型返回 false", func() {
			_, ok := AssetKindOf("never-registered")
			So(ok, ShouldBeFalse)
		})
	})
}
