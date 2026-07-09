package runner

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestPromptBuilderBuild(t *testing.T) {
	Convey("PromptBuilder.Build", t, func() {
		Convey("无 OpenTabs 时输出语言提示（角色描述已搬到 system_template.go）", func() {
			got := NewPromptBuilder("zh-cn", AIContext{}).Build()
			So(got, ShouldContainSubstring, "Chinese")
			So(got, ShouldNotContainSubstring, "OpsKat AI assistant")
		})

		Convey("OpenTabs 渲染包含每个 tab 名和 ID", func() {
			got := NewPromptBuilder("en", AIContext{
				OpenTabs: []TabInfo{
					{Type: "ssh", AssetID: 42, AssetName: "prod-db"},
					{Type: "database", AssetID: 43, AssetName: "metrics"},
				},
			}).Build()
			So(got, ShouldContainSubstring, "SSH Terminal")
			So(got, ShouldContainSubstring, "prod-db")
			So(got, ShouldContainSubstring, "Database Query")
			So(got, ShouldContainSubstring, "metrics")
		})

		Convey("OpenTabs 不再输出 primary/active 路由提示", func() {
			ctx := AIContext{OpenTabs: []TabInfo{
				{Type: "ssh", AssetID: 1, AssetName: "prod-web-01"},
				{Type: "ssh", AssetID: 2, AssetName: "other"},
			}}
			out := NewPromptBuilder("en", ctx).Build()
			So(out, ShouldContainSubstring, "prod-web-01")
			So(out, ShouldContainSubstring, "other")
			So(out, ShouldNotContainSubstring, "PRIMARY")
			So(out, ShouldNotContainSubstring, "prefer it when the target is ambiguous")
		})

		Convey("输出内联 mention 语义提示", func() {
			got := NewPromptBuilder("en", AIContext{}).Build()
			So(got, ShouldContainSubstring, "<mention")
			So(got, ShouldContainSubstring, "asset-id")
			So(got, ShouldContainSubstring, "database")
			So(got, ShouldContainSubstring, "table")
		})

		Convey("Extension SKILL.md 被注入", func() {
			b := NewPromptBuilder("en", AIContext{})
			b.SetExtensionSkillMDs(map[string]string{"k8s": "k8s skill body"})
			got := b.Build()
			So(got, ShouldContainSubstring, "From extension: k8s")
			So(got, ShouldContainSubstring, "k8s skill body")
		})
	})
}
