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
			// 用 "widget" 而非某个真实资产类型作占位符：注册/注销的是这个键本身，
			// 借用一个已被真实类型占用的键（如 "oss"）会在测试结束时把该类型 init()
			// 里注册的默认策略也一并删掉，污染同进程内其它测试。
			RegisterDefaultPolicy("widget", func() any {
				return &CommandPolicy{Groups: []string{"ext:widget:readonly"}}
			})
			defer UnregisterDefaultPolicy("widget")

			p, ok := GetDefaultPolicyOf("widget")
			So(ok, ShouldBeTrue)
			cp, ok := p.(*CommandPolicy)
			So(ok, ShouldBeTrue)
			So(cp.Groups, ShouldResemble, []string{"ext:widget:readonly"})

			UnregisterDefaultPolicy("widget")
			_, ok = GetDefaultPolicyOf("widget")
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

// TestOSSDefaultPolicyRegisteredByInit 守的是注册表接线本身：registry.go 的 init() 真的
// 调用了 RegisterDefaultPolicy("oss", ...)，且挂的 provider 真的返回 *OSSPolicy——这两件事
// 只有经过 GetDefaultPolicyOf 这条查询路径才验证得到，直接调用 DefaultOSSPolicy()（如
// policy_test.go 的 TestDefaultOSSPolicy）绕过了注册表，测不出 init() 里那一行被删掉。
// 不在这里断言 Groups 的具体内容——那是 DefaultOSSPolicy() 自己的返回值，已经由
// TestDefaultOSSPolicy 覆盖，在这里重复断言只是抄一遍同一份事实，两处都要改这个测试才会跟着
// 失败，检测不出新的缺陷（AGENTS.md Fix policy）。
func TestOSSDefaultPolicyRegisteredByInit(t *testing.T) {
	Convey("oss 的默认策略已在 init() 中注册,消费方无需再自行调用 DefaultOSSPolicy()", t, func() {
		p, ok := GetDefaultPolicyOf("oss")
		So(ok, ShouldBeTrue)

		_, ok = p.(*OSSPolicy)
		So(ok, ShouldBeTrue)
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
