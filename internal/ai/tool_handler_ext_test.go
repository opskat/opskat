package ai

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestExecToolHandler(t *testing.T) {
	Convey("handleExecTool", t, func() {
		// Reset global state
		origExecutor := execToolExecutor
		t.Cleanup(func() { execToolExecutor = origExecutor })

		Convey("should return error when no executor configured", func() {
			execToolExecutor = nil
			_, err := handleExecTool(t.Context(), map[string]any{
				"extension": "nonexistent",
				"tool":      "some_tool",
				"args":      map[string]any{},
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not found")
		})

		Convey("should return error when missing extension arg", func() {
			_, err := handleExecTool(t.Context(), map[string]any{
				"tool": "some_tool",
				"args": map[string]any{},
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "extension")
		})

		Convey("should return error when missing tool arg", func() {
			_, err := handleExecTool(t.Context(), map[string]any{
				"extension": "oss",
				"args":      map[string]any{},
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "tool")
		})
	})
}

func TestPromptBuilderExtensionSkillMD(t *testing.T) {
	Convey("PromptBuilder with extension SKILL.md", t, func() {
		builder := NewPromptBuilder("en", AIContext{})

		Convey("should not include extension content by default", func() {
			prompt := builder.Build()
			So(prompt, ShouldNotContainSubstring, "exec_tool")
		})

		Convey("should include SKILL.md when set", func() {
			builder.SetExtensionSkillMD("# OSS Tools\nUse exec_tool to call OSS tools.")
			prompt := builder.Build()
			So(prompt, ShouldContainSubstring, "OSS Tools")
			So(prompt, ShouldContainSubstring, "exec_tool")
		})
	})
}
