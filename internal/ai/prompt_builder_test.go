package ai

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBuildMentionContext(t *testing.T) {
	Convey("buildMentionContext 渲染", t, func() {
		Convey("空 MentionedAssets 返回空串", func() {
			b := NewPromptBuilder("zh-cn", AIContext{})
			So(b.buildMentionContext(), ShouldEqual, "")
		})

		Convey("单个资产渲染", func() {
			b := NewPromptBuilder("zh-cn", AIContext{
				MentionedAssets: []MentionedAsset{
					{AssetID: 42, Name: "prod-db", Type: "mysql", Host: "10.0.0.5", GroupPath: "生产/数据库"},
				},
			})
			got := b.buildMentionContext()
			So(got, ShouldContainSubstring, "Assets referenced in the user's message")
			So(got, ShouldContainSubstring, "@prod-db")
			So(got, ShouldContainSubstring, "ID=42")
			So(got, ShouldContainSubstring, "type=mysql")
			So(got, ShouldContainSubstring, "host=10.0.0.5")
			So(got, ShouldContainSubstring, "group=生产/数据库")
		})

		Convey("多个资产每项独占一行", func() {
			b := NewPromptBuilder("zh-cn", AIContext{
				MentionedAssets: []MentionedAsset{
					{AssetID: 42, Name: "a", Type: "ssh", Host: "1.1.1.1"},
					{AssetID: 43, Name: "b", Type: "redis", Host: "2.2.2.2"},
				},
			})
			got := b.buildMentionContext()
			lines := strings.Split(got, "\n")
			bulletCount := 0
			for _, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), "- @") {
					bulletCount++
				}
			}
			So(bulletCount, ShouldEqual, 2)
		})

		Convey("GroupPath 为空不输出 group 字段", func() {
			b := NewPromptBuilder("zh-cn", AIContext{
				MentionedAssets: []MentionedAsset{
					{AssetID: 42, Name: "x", Type: "ssh", Host: "1.1.1.1"},
				},
			})
			So(b.buildMentionContext(), ShouldNotContainSubstring, "group=")
		})
	})
}

func TestBuildIncludesMentionContext(t *testing.T) {
	Convey("Build 结果包含 mention 段", t, func() {
		b := NewPromptBuilder("zh-cn", AIContext{
			MentionedAssets: []MentionedAsset{
				{AssetID: 1, Name: "srv", Type: "ssh", Host: "h"},
			},
		})
		got := b.Build()
		So(got, ShouldContainSubstring, "@srv")
		So(got, ShouldContainSubstring, "ID=1")
	})
}

func TestRenderMentionContext(t *testing.T) {
	Convey("RenderMentionContext", t, func() {
		Convey("空切片返回空串", func() {
			So(RenderMentionContext(nil), ShouldEqual, "")
			So(RenderMentionContext([]MentionedAsset{}), ShouldEqual, "")
		})
		Convey("非空切片与 buildMentionContext 输出一致", func() {
			mentions := []MentionedAsset{
				{AssetID: 7, Name: "srv", Type: "ssh", Host: "h", GroupPath: "prod"},
			}
			direct := RenderMentionContext(mentions)
			viaBuilder := NewPromptBuilder("en", AIContext{MentionedAssets: mentions}).buildMentionContext()
			So(direct, ShouldEqual, viaBuilder)
			So(direct, ShouldContainSubstring, "Assets referenced in the user's message")
		})
	})
}
