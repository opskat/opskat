package opskat

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCommandReviewer(t *testing.T) {
	questions := []ReviewQuestion{{ID: "destructive", Name: map[string]string{"zh-CN": "破坏数据", "en": "Destroys data"}}}

	Convey("CommandReviewer 注册后 describe 与 review_commands 共用同一份声明", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "x"})
		AssetType[struct {
			Endpoint string `json:"endpoint"`
		}]("x")

		var got []ReviewItem
		CommandReviewer(questions, 8, 100, func(_ *ReviewContext, items []ReviewItem) ([]ReviewResult, error) {
			got = items
			return []ReviewResult{{Outcome: ReviewReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.2}}}, nil
		})

		desc := decodeDescribe(t)
		rev := desc["reviewer"].(map[string]any)
		So(rev["maxItems"], ShouldEqual, float64(8))
		So(rev["maxCommandLen"], ShouldEqual, float64(100))
		qs := rev["questions"].([]any)
		So(len(qs), ShouldEqual, 1)
		So(qs[0].(map[string]any)["id"], ShouldEqual, "destructive")

		raw, err := dispatch("review_commands", []byte(`{"items":[{"assetType":"ssh","command":"rm -rf /tmp/x","syntax":"shell"}]}`))
		So(err, ShouldBeNil)
		So(len(got), ShouldEqual, 1)
		So(got[0].Command, ShouldEqual, "rm -rf /tmp/x")
		So(got[0].Syntax, ShouldEqual, "shell")

		var results []ReviewResult
		So(json.Unmarshal(raw, &results), ShouldBeNil)
		So(results[0].Outcome, ShouldEqual, ReviewReject)
		So(results[0].Failed, ShouldResemble, []string{"destructive"})

		Convey("没有注册时 review_commands 失败，describe 不含 reviewer", func() {
			resetRegistries()
			Extension(Meta{PolicyType: "x"})
			AssetType[struct {
				Endpoint string `json:"endpoint"`
			}]("x")
			_, err := dispatch("review_commands", []byte(`{"items":[]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "no command reviewer")
			_, has := decodeDescribe(t)["reviewer"]
			So(has, ShouldBeFalse)
		})
	})
}

func TestCommandReviewerRejectsBadRegistration(t *testing.T) {
	Convey("不合法的审核器在注册时失败", t, func() {
		resetRegistries()
		fn := func(_ *ReviewContext, _ []ReviewItem) ([]ReviewResult, error) { return nil, nil }
		So(func() { CommandReviewer(nil, 1, 1, fn) }, ShouldPanic)
		So(func() {
			CommandReviewer([]ReviewQuestion{{ID: "a", Name: map[string]string{"en": "A"}}}, 0, 1, fn)
		}, ShouldPanic)
		So(func() {
			CommandReviewer([]ReviewQuestion{{ID: "a"}}, 1, 1, fn)
		}, ShouldPanic)
	})
}
