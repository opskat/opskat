package opskat

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/pkg/extwire"
)

func TestCommandReviewer(t *testing.T) {
	questions := []ReviewQuestion{
		{ID: "destructive", Names: map[string]string{"en": "may destroy data", "zh-CN": "可能破坏数据"}},
	}
	review := func(_ *ToolContext, item ReviewItem) ReviewResult {
		if strings.Contains(item.Command, "rm -rf") {
			return ReviewResult{Outcome: ReviewReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.9}}
		}
		return ReviewResult{Outcome: ReviewPass}
	}

	Convey("CommandReviewer declares the reviewer and its host-only review tool", t, func() {
		resetRegistries()
		CommandReviewer(questions, 4000, review)

		contributes := decodeContributes(t)
		So(contributes["commandReviewer"], ShouldResemble, map[string]any{
			"tool":          ReviewToolName,
			"questions":     []any{map[string]any{"id": "destructive", "names": map[string]any{"en": "may destroy data", "zh-CN": "可能破坏数据"}}},
			"maxCommandLen": float64(4000),
		})
		tools := contributes["tools"].([]any)
		So(tools, ShouldHaveLength, 1)
		tool := tools[0].(map[string]any)
		So(tool["name"], ShouldEqual, ReviewToolName)
		So(tool["hostOnly"], ShouldEqual, true)
		params := tool["parameters"].(map[string]any)
		So(params["required"], ShouldResemble, []any{"assetType", "command", "syntax"})
		props := params["properties"].(map[string]any)
		So(props, ShouldHaveLength, 3)
		for _, name := range []string{"assetType", "command", "syntax"} {
			So(props[name].(map[string]any)["type"], ShouldEqual, "string")
		}
	})

	Convey("the review tool's limit admits the longest review timeout the host lets the user set", t, func() {
		resetRegistries()
		CommandReviewer(questions, 4000, review)

		tool := decodeContributes(t)["tools"].([]any)[0].(map[string]any)
		So(tool["timeoutMs"], ShouldEqual, float64(extwire.MaxReviewTimeout.Milliseconds()))
	})

	Convey("the review tool answers one command with the function's verdict", t, func() {
		resetRegistries()
		CommandReviewer(questions, 4000, review)
		th := NewTestHost()
		defer th.Close()

		answer := func(command string) ReviewResult {
			got, err := th.CallTool(Asset{}, ReviewToolName, ReviewItem{AssetType: "ssh", Command: command, Syntax: "shell"})
			So(err, ShouldBeNil)
			raw, err := json.Marshal(got)
			So(err, ShouldBeNil)
			var r ReviewResult
			So(json.Unmarshal(raw, &r), ShouldBeNil)
			return r
		}
		So(answer("rm -rf /"), ShouldResemble, ReviewResult{Outcome: ReviewReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.9}})
		So(answer("ls"), ShouldResemble, ReviewResult{Outcome: ReviewPass})
	})

	Convey("an extension has one command reviewer", t, func() {
		resetRegistries()
		CommandReviewer(questions, 4000, review)
		So(func() { CommandReviewer(questions, 4000, review) }, ShouldPanicWith, "opskat: a command reviewer is already declared")
	})

	Convey("no reviewer is reported when none is declared", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "demo"})
		So(decodeContributes(t), ShouldNotContainKey, "commandReviewer")
	})
}
