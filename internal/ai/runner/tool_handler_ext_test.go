package runner

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestPromptBuilderExtensionSkillMD(t *testing.T) {
	Convey("PromptBuilder with extension SKILL.md", t, func() {
		builder := NewPromptBuilder("en", AIContext{})

		Convey("should not include extension content by default", func() {
			prompt := builder.Build()
			So(prompt, ShouldNotContainSubstring, "exec_tool")
		})

		Convey("should include SKILL.md when set", func() {
			builder.SetExtensionSkillMDs(map[string]string{
				"oss": "# OSS Tools\nUse exec_tool to call OSS tools.",
			})
			prompt := builder.Build()
			So(prompt, ShouldContainSubstring, "OSS Tools")
			So(prompt, ShouldContainSubstring, "exec_tool")
		})
	})
}
